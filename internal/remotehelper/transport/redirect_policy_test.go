package transport

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/internal/entireclient/httpclient"
	"github.com/entireio/cli/internal/remotehelper/replicas"
)

func TestProxy_RedirectRejectionPreservesReplicaHealth(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		cold, loop bool
	}{
		{"warm downgrade", false, false}, {"cold downgrade", true, false},
		{"warm redirect loop", false, true}, {"cold redirect loop", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			const entry = "https://cluster.example"
			const node = "https://n.cluster.example"
			nodes := []string{node}
			if tc.cold {
				nodes = nil
			}
			var failed []string
			p := New(Config{Nodes: replicas.NodeConfig{EntryURL: entry, ClusterHost: "cluster.example", InitialNodes: nodes}, OnNodeFailed: func(node string) { failed = append(failed, node) }})
			calls := 0
			rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				h := make(http.Header)
				h.Set("Location", "http://n.cluster.example/repo")
				if tc.loop {
					h.Set("Location", node+"/repo")
				}
				if req.URL.Host == "cluster.example" {
					h.Set("X-Entire-Replicas", node)
					h.Set("Location", node+"/repo")
				}
				return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: h, Body: http.NoBody, Request: req}, nil
			})
			p.client.Transport = rt
			p.discoveryTransport = rt
			_, err := p.InfoRefs(t.Context(), "git-upload-pack")
			wantErr := httpclient.ErrInsecureRedirect
			if tc.loop {
				wantErr = httpclient.ErrTooManyRedirects
			}
			require.ErrorIs(t, err, wantErr)
			require.Empty(t, failed, "policy failures must not invoke the production cache-invalidation hook")
			require.Equal(t, []string{node}, p.nodes)
			wantCalls := 1
			if tc.loop {
				wantCalls = 10
			}
			if tc.cold {
				wantCalls++
			}
			require.Equal(t, wantCalls, calls, "policy failures must not trigger entry/Location fallback")
		})
	}
}

func TestProxy_SkippedReplicaPreservesStorageError(t *testing.T) {
	t.Parallel()
	const secure = "https://127.0.0.1"
	const plain = "http://127.0.0.1"
	p := New(Config{Nodes: replicas.NodeConfig{EntryURL: plain, InitialNodes: []string{secure, plain}}})
	p.stickyNode = secure
	p.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, "https", req.URL.Scheme)
		return &http.Response{StatusCode: http.StatusInsufficientStorage, Header: make(http.Header), Body: http.NoBody, Request: req}, nil
	})
	_, err := p.ServiceRPC(t.Context(), "git-receive-pack", strings.NewReader("push"))
	var storage *InsufficientStorageError
	require.ErrorAs(t, err, &storage)
}

func TestProxy_ResponseURLFallbackRequiresSafeScheme(t *testing.T) {
	t.Parallel()
	p := New(Config{Nodes: replicas.NodeConfig{EntryURL: "https://cluster.example", ClusterHost: "cluster.example"}})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://cluster.example/repo", nil)
	require.NoError(t, err)
	p.refreshReplicas(&http.Response{Header: make(http.Header), Request: req})
	require.Empty(t, p.nodes)
}
