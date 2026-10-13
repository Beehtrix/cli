package transport

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/internal/entireclient/httpclient"
	"github.com/entireio/cli/internal/remotehelper/replicas"
)

func TestProxy_ColdProbeUsesHTTPSReplicasDespiteHTTPLocation(t *testing.T) {
	t.Parallel()
	var plaintextHits atomic.Int32
	plaintext := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		plaintextHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(plaintext.Close)
	replica := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, err := io.WriteString(w, "refs"); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(replica.Close)
	entry := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Entire-Replicas", replica.URL)
		http.Redirect(w, r, plaintext.URL, http.StatusTemporaryRedirect)
	}))
	t.Cleanup(entry.Close)
	p := New(Config{Nodes: replicas.NodeConfig{EntryURL: entry.URL, ClusterHost: mustHost(t, entry.URL)}, SetAuth: func(req *http.Request) error {
		req.Header.Set("Authorization", "Bearer test-token")
		return nil
	}})
	p.discoveryTransport = entry.Client().Transport
	p.client.Transport = replica.Client().Transport
	body, err := p.InfoRefs(t.Context(), "git-upload-pack")
	require.NoError(t, err)
	defer body.Close()
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	require.Equal(t, "refs", string(data))
	require.Zero(t, plaintextHits.Load())
	require.Equal(t, []string{replica.URL}, p.nodes)
}

func TestProxy_PolicyRejectionTriesRemainingReplicas(t *testing.T) {
	t.Parallel()
	const bad = "https://bad.cluster.example"
	const good = "https://good.cluster.example"
	var failed, dialed []string
	p := New(Config{Nodes: replicas.NodeConfig{EntryURL: "https://cluster.example", ClusterHost: "cluster.example", InitialNodes: []string{bad, good}}, OnNodeFailed: func(node string) { failed = append(failed, node) }})
	p.stickyNode = bad
	p.client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		node := req.URL.Scheme + "://" + req.URL.Host
		dialed = append(dialed, node)
		code := http.StatusOK
		h := make(http.Header)
		if node == bad {
			code = http.StatusTemporaryRedirect
			h.Set("Location", "http://bad.cluster.example/repo")
		}
		return &http.Response{StatusCode: code, Header: h, Body: http.NoBody, Request: req}, nil
	})
	body, err := p.InfoRefs(t.Context(), "git-upload-pack")
	require.NoError(t, err)
	require.NoError(t, body.Close())
	require.Equal(t, []string{bad, good}, dialed)
	require.Empty(t, failed)
	require.Equal(t, []string{bad, good}, p.nodes)
}

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

func TestProxy_InsecureReplicaErrorDoesNotClaimRedirect(t *testing.T) {
	t.Parallel()
	p := New(Config{Nodes: replicas.NodeConfig{EntryURL: "http://cluster.example"}})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://cluster.example/repo", nil)
	require.NoError(t, err)
	err = p.setAuthOrError(req)
	require.ErrorIs(t, err, ErrInsecureReplica)
	require.ErrorIs(t, err, httpclient.ErrRequestPolicy)
	require.NotContains(t, err.Error(), "redirect")
}

func TestProxy_ResponseURLFallbackRequiresSafeScheme(t *testing.T) {
	t.Parallel()
	p := New(Config{Nodes: replicas.NodeConfig{EntryURL: "https://cluster.example", ClusterHost: "cluster.example"}})
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://cluster.example/repo", nil)
	require.NoError(t, err)
	p.refreshReplicas(&http.Response{Header: make(http.Header), Request: req})
	require.Empty(t, p.nodes)
}
