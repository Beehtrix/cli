package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/internal/coreapi"
)

// fakeMirrorSyncReader scripts the two reads the block lookup makes: the
// placement lookup (mirror IDs, or none) and the mirror record behind the
// first one. It records what was asked so a test can count the reads.
type fakeMirrorSyncReader struct {
	mu         sync.Mutex
	placements []string                  // mirror IDs the placement lookup returns
	mirrors    map[string]coreapi.Mirror // by mirror ID
	err        error
	resolves   []coreapi.ResolveMirrorPlacementsParams
	gets       []string
}

func (f *fakeMirrorSyncReader) ResolveMirrorPlacements(_ context.Context, p coreapi.ResolveMirrorPlacementsParams) (*coreapi.ResolvePlacementsOutputBody, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolves = append(f.resolves, p)
	if f.err != nil {
		return nil, f.err
	}
	out := &coreapi.ResolvePlacementsOutputBody{}
	for _, id := range f.placements {
		out.Placements = append(out.Placements, coreapi.ResolvedPlacement{MirrorId: id, ClusterHost: defaultClusterHost})
	}
	return out, nil
}

func (f *fakeMirrorSyncReader) GetMirror(_ context.Context, p coreapi.GetMirrorParams) (*coreapi.Mirror, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gets = append(f.gets, p.MirrorId)
	if f.err != nil {
		return nil, f.err
	}
	m, ok := f.mirrors[p.MirrorId]
	if !ok {
		return nil, errors.New("no such mirror")
	}
	return &m, nil
}

var testBlockObservedAt = time.Date(2026, 10, 10, 17, 48, 0, 0, time.UTC)

func mirrorOf(owner, repo string) coreapi.Mirror {
	return coreapi.Mirror{MirrorId: "m-" + owner + "-" + repo, Owner: owner, Repo: repo, Provider: repoProviderGitHub, ClusterHost: defaultClusterHost}
}

// blockedMirrorOf is an owner's mirror stamped with the org IP-allow-list
// block at testBlockObservedAt — the one block every end-to-end case here
// renders, so the fixtures read as what they are.
func blockedMirrorOf(owner string) coreapi.Mirror {
	m := mirrorOf(owner, "web")
	m.SyncBlock = coreapi.NewOptMirrorSyncBlock(coreapi.MirrorSyncBlock{Reason: syncBlockOrgIPAllowlist, ObservedAt: testBlockObservedAt})
	return m
}

func TestLookupMirrorSyncBlock(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	wantBlock := mirrorSyncBlock{Reason: syncBlockOrgIPAllowlist, ObservedAt: testBlockObservedAt}

	t.Run("reads the first placement's mirror record for the block", func(t *testing.T) {
		t.Parallel()
		f := &fakeMirrorSyncReader{
			placements: []string{"m-eu", "m-us"},
			mirrors:    map[string]coreapi.Mirror{"m-eu": blockedMirrorOf("acme"), "m-us": blockedMirrorOf("acme")},
		}
		block, blocked, err := lookupMirrorSyncBlock(ctx, f, "/gh/acme/web")
		require.NoError(t, err)
		require.True(t, blocked)
		require.Equal(t, wantBlock, block)
		require.Len(t, f.resolves, 1)
		require.Equal(t, "acme", f.resolves[0].Owner)
		require.Equal(t, "web", f.resolves[0].Repo)
		require.Equal(t, []string{"m-eu"}, f.gets, "one placement answers for the whole installation")
	})

	t.Run("an unblocked mirror and a repo with nothing pullable both read as not blocked", func(t *testing.T) {
		t.Parallel()
		f := &fakeMirrorSyncReader{placements: []string{"m"}, mirrors: map[string]coreapi.Mirror{"m": mirrorOf("acme", "web")}}
		_, blocked, err := lookupMirrorSyncBlock(ctx, f, "/gh/acme/web")
		require.NoError(t, err)
		require.False(t, blocked)

		f = &fakeMirrorSyncReader{}
		_, blocked, err = lookupMirrorSyncBlock(ctx, f, "/gh/acme/web")
		require.NoError(t, err)
		require.False(t, blocked)
		require.Empty(t, f.gets, "no placement, so no mirror read")
	})

	t.Run("a failed read is an error, not an absent block", func(t *testing.T) {
		t.Parallel()
		f := &fakeMirrorSyncReader{err: errors.New("boom")}
		_, _, err := lookupMirrorSyncBlock(ctx, f, "/gh/acme/web")
		require.EqualError(t, err, "resolve mirror placements: boom")
	})
}

func TestMirrorRefRepo(t *testing.T) {
	t.Parallel()
	require.Equal(t, "web", mirrorRefRepo("/gh/acme/web"))
	require.Equal(t, "web", mirrorRefRepo("gh/acme/web"))
	require.Equal(t, "web", mirrorRefRepo("/et/acme/web"))
	require.Empty(t, mirrorRefRepo("/gh/acme"))
}

func TestNoteSyncStatusUnavailable(t *testing.T) {
	t.Parallel()

	t.Run("nil passes through silently", func(t *testing.T) {
		t.Parallel()
		var errW strings.Builder
		require.NoError(t, noteSyncStatusUnavailable(t.Context(), &errW, nil))
		require.Empty(t, errW.String())
	})

	t.Run("a server failure becomes a note and no error", func(t *testing.T) {
		t.Parallel()
		var errW strings.Builder
		require.NoError(t, noteSyncStatusUnavailable(t.Context(), &errW, errors.New("read mirrors: boom")))
		require.Equal(t, "Note: GitHub sync status unavailable: read mirrors: boom\n", errW.String())
	})

	t.Run("an interruption is returned, not noted", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		var errW strings.Builder
		err := noteSyncStatusUnavailable(ctx, &errW, fmt.Errorf("read mirrors: %w", context.Canceled))
		require.ErrorIs(t, err, context.Canceled)
		require.Empty(t, errW.String())
		// The wrapped cause alone is enough: a transport error that carries
		// the cancellation must not be downgraded because ctx looks live.
		err = noteSyncStatusUnavailable(t.Context(), &errW, fmt.Errorf("read mirrors: %w", context.DeadlineExceeded))
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Empty(t, errW.String())
	})
}

func TestSyncBlockWording(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		reason       string
		problem, fix string
	}{
		{syncBlockOrgIPAllowlist, "IP allow list of the acme organization blocks Entire", "Ask an admin of the acme organization"},
		{syncBlockInstallationSuspended, "is suspended", "Ask an admin of acme on GitHub to unsuspend"},
		{"something_new", "(something_new)", "restore the Entire app's access"},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			t.Parallel()
			require.Contains(t, syncBlockProblem("acme", tc.reason), tc.problem)
			require.Contains(t, syncBlockFix("acme", tc.reason), tc.fix)
			if tc.reason != "something_new" {
				require.NotContains(t, syncBlockProblem("acme", tc.reason), tc.reason, "a known reason is spelled for the org admin, never as Entire's enum")
			}
		})
	}
}

// resyncProblem is the 409 the server answers while GitHub still blocks the
// installation: the customer-facing message in detail, the reason at body.owner.
func resyncProblem(reason string) string {
	return fmt.Sprintf(`{"title":"Conflict","status":409,"detail":"GitHub blocks the Entire installation for this organization","errors":[{"location":"body.owner","value":%q}]}`, reason)
}

// serveResync fakes POST /mirrors/resync: status and body are what it answers,
// and the request body it received comes back for assertions.
func serveResync(t *testing.T, status int, body string) (srvURL string, got *coreapi.ResyncMirrorInputBody) {
	t.Helper()
	got = &coreapi.ResyncMirrorInputBody{}
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/mirrors/resync" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		raw, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		mu.Lock()
		assert.NoError(t, json.Unmarshal(raw, got), "decode resync body %s", raw)
		mu.Unlock()
		if status >= 400 {
			w.Header().Set("Content-Type", "application/problem+json")
		} else {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(status)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, got
}

// Not parallel: swaps the package-level activeCoreClient seam.
func TestRepoMirrorResync(t *testing.T) {
	const accepted = `{"mirrorId":"01MIRROR","status":"queued","later":"kept"}`

	t.Run("queues a resync of every placement and says so", func(t *testing.T) {
		srvURL, got := serveResync(t, http.StatusAccepted, accepted)
		stdout, _, err := runCoreCmd(t, newRepoMirrorResyncCmd, srvURL, "/gh/octocat/hello-world")
		require.NoError(t, err)
		require.Equal(t, coreapi.ResyncMirrorInputBodyProviderGithub, got.Provider)
		require.Equal(t, "octocat", got.Owner)
		require.Equal(t, "hello-world", got.Repo)
		require.Equal(t, "Resync queued for /gh/octocat/hello-world on every cluster it is mirrored on.\n", stdout)
	})

	t.Run("--json is the wire object plus the repo, fields the server adds included", func(t *testing.T) {
		srvURL, _ := serveResync(t, http.StatusAccepted, accepted)
		stdout, _, err := runCoreCmd(t, newRepoMirrorResyncCmd, srvURL, "/gh/octocat/hello-world", "--json")
		require.NoError(t, err)
		var res map[string]any
		require.NoError(t, json.Unmarshal([]byte(stdout), &res))
		require.Equal(t, map[string]any{"repo": "/gh/octocat/hello-world", "mirrorId": "01MIRROR", "status": "queued", "later": "kept"}, res)
	})

	t.Run("a still-blocked installation reports the reason and the fix, in the view's words", func(t *testing.T) {
		srvURL, _ := serveResync(t, http.StatusConflict, resyncProblem(syncBlockOrgIPAllowlist))
		_, _, err := runCoreCmd(t, newRepoMirrorResyncCmd, srvURL, "/gh/octocat/hello-world")
		require.Error(t, err)
		require.Contains(t, err.Error(), syncBlockProblem("octocat", syncBlockOrgIPAllowlist))
		require.Contains(t, err.Error(), "Nothing was queued")
		require.Contains(t, err.Error(), syncBlockFix("octocat", syncBlockOrgIPAllowlist))
		require.Contains(t, err.Error(), "run this command again")
	})

	t.Run("a 409 without the reason detail keeps the server's own message", func(t *testing.T) {
		srvURL, _ := serveResync(t, http.StatusConflict, `{"title":"Conflict","status":409,"detail":"mirror is being detached"}`)
		_, _, err := runCoreCmd(t, newRepoMirrorResyncCmd, srvURL, "/gh/octocat/hello-world")
		require.EqualError(t, err, "mirror is being detached")
	})

	t.Run("other refusals keep the server's own message", func(t *testing.T) {
		srvURL, _ := serveResync(t, http.StatusForbidden, `{"title":"Forbidden","status":403,"detail":"write access to the GitHub repository is required"}`)
		_, _, err := runCoreCmd(t, newRepoMirrorResyncCmd, srvURL, "/gh/octocat/hello-world")
		require.EqualError(t, err, "write access to the GitHub repository is required")
	})

	t.Run("serves /gh/ refs only, and refuses before dialing", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Errorf("a refused ref dialed the core: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}))
		t.Cleanup(srv.Close)
		_, _, err := runCoreCmd(t, newRepoMirrorResyncCmd, srv.URL, "/et/acme/web")
		require.ErrorContains(t, err, "/et/")
		_, _, err = runCoreCmd(t, newRepoMirrorResyncCmd, srv.URL, "octocat/hello-world")
		require.ErrorContains(t, err, "/gh/octocat/hello-world")
	})
}

// blockedDirectory is a fake control plane for a GitHub directory whose owner
// acme is blocked. It serves /repos, /clusters, the placement lookup and the
// mirror record behind it — the two reads `view` makes for the block.
type blockedDirectory struct {
	repos      []coreapi.RepoIndexEntry
	syncStatus int // >= 400 fails the placement lookup with a problem
	onSync     func()
	syncReads  int
	mu         sync.Mutex
}

func (d *blockedDirectory) serve(t *testing.T) {
	t.Helper()
	clusters := []coreapi.Cluster{{Slug: "us", PublicUrl: "https://" + defaultClusterHost}}
	reader := &fakeMirrorSyncReader{
		placements: []string{"m-acme"},
		mirrors:    map[string]coreapi.Mirror{"m-acme": blockedMirrorOf("acme"), "m-other": mirrorOf("other", "tool")},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == testClustersPath:
			writeJSONResponse(t, w, http.StatusOK, &coreapi.ListClustersOutputBody{Clusters: clusters})
		case r.URL.Path == testReposPath:
			writeJSONResponse(t, w, http.StatusOK, &coreapi.ListReposOutputBody{Repos: d.repos})
		case r.URL.Path == testMirrorPlacementsPath:
			d.mu.Lock()
			d.syncReads++
			d.mu.Unlock()
			if d.onSync != nil {
				d.onSync()
			}
			if d.syncStatus >= 400 {
				writeCoreProblem(t, w, d.syncStatus, "placements unavailable")
				return
			}
			owner, repo := r.URL.Query().Get("owner"), r.URL.Query().Get("repo")
			ids := reader.placements
			if owner != "acme" {
				ids = []string{"m-" + owner} // other owners: one unblocked mirror
			}
			out, err := (&fakeMirrorSyncReader{placements: ids}).ResolveMirrorPlacements(r.Context(), coreapi.ResolveMirrorPlacementsParams{Owner: owner, Repo: repo})
			assert.NoError(t, err)
			writeJSONResponse(t, w, http.StatusOK, out)
		case strings.HasPrefix(r.URL.Path, "/api/v1/mirrors/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/v1/mirrors/")
			m, err := reader.GetMirror(r.Context(), coreapi.GetMirrorParams{MirrorId: id})
			if err != nil {
				writeCoreProblem(t, w, http.StatusNotFound, err.Error())
				return
			}
			writeJSONResponse(t, w, http.StatusOK, m)
		default:
			t.Errorf("unexpected path %q", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	seamActive(t, func(context.Context) (*coreapi.Client, error) {
		return coreapi.NewWithBearer(srv.URL, "tok")
	})
}

func (d *blockedDirectory) reads() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.syncReads
}

// Not parallel: swaps the package-level activeCoreClient seam.
func TestRepoView_SyncBlock(t *testing.T) {
	web := []coreapi.RepoIndexEntry{onboardedEntry("acme/web", "private", "us")}

	t.Run("a blocked owner's mirror shows the block, the fix and the resync command", func(t *testing.T) {
		(&blockedDirectory{repos: web}).serve(t)
		out, err := runRepoView(t, "/gh/acme/web")
		require.NoError(t, err)
		requireOrder(t, out,
			"Name:", "/gh/acme/web",
			"Sync:", "Blocked (observed",
			syncBlockProblem("acme", syncBlockOrgIPAllowlist),
			syncBlockFix("acme", syncBlockOrgIPAllowlist),
			"entire repo mirror resync /gh/acme/web",
			"CLUSTER", defaultClusterHost, "ready",
		)
		require.NotContains(t, out, syncBlockOrgIPAllowlist, "the enum stays off the record; the sentence says what it means")
	})

	t.Run("--json carries the block on the row", func(t *testing.T) {
		(&blockedDirectory{repos: web}).serve(t)
		out, err := runRepoView(t, "/gh/acme/web", "--json")
		require.NoError(t, err)
		var got struct {
			SyncBlock *mirrorSyncBlock `json:"syncBlock"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &got))
		require.Equal(t, &mirrorSyncBlock{Reason: syncBlockOrgIPAllowlist, ObservedAt: testBlockObservedAt}, got.SyncBlock)
	})

	t.Run("an unblocked owner shows no Sync section and no syncBlock key", func(t *testing.T) {
		(&blockedDirectory{repos: []coreapi.RepoIndexEntry{onboardedEntry("other/tool", "private", "us")}}).serve(t)
		out, err := runRepoView(t, "/gh/other/tool")
		require.NoError(t, err)
		require.NotContains(t, out, "Sync:")
		out, err = runRepoView(t, "/gh/other/tool", "--json")
		require.NoError(t, err)
		require.NotContains(t, out, "syncBlock")
	})

	t.Run("a failed lookup is a note on stderr, not a failed view", func(t *testing.T) {
		(&blockedDirectory{repos: web, syncStatus: http.StatusServiceUnavailable}).serve(t)
		cmd := newRepoCmd()
		var out, errW strings.Builder
		cmd.SetOut(&out)
		cmd.SetErr(&errW)
		cmd.SetArgs([]string{"view", "/gh/acme/web"})
		require.NoError(t, cmd.ExecuteContext(t.Context()))
		require.Contains(t, out.String(), "ready")
		require.NotContains(t, out.String(), "Sync:")
		require.Equal(t, "Note: GitHub sync status unavailable: placements unavailable\n", errW.String())
	})

	t.Run("an interruption during the lookup fails the view", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		(&blockedDirectory{repos: web, onSync: cancel}).serve(t)
		cmd := newRepoCmd()
		var out, errW strings.Builder
		cmd.SetOut(&out)
		cmd.SetErr(&errW)
		cmd.SetArgs([]string{"view", "/gh/acme/web"})
		err := cmd.ExecuteContext(ctx)
		require.ErrorIs(t, err, context.Canceled)
		require.NotContains(t, errW.String(), "sync status unavailable", "an interruption is not a note")
		require.NotContains(t, out.String(), "CLUSTER", "nothing is rendered over an interruption")
	})
}

// Not parallel: swaps the package-level activeCoreClient seam.
func TestRepoMirrorList_NoSyncBlock(t *testing.T) {
	// The directory never reads the block: it would cost a mirror read per
	// repo, and the view is the record the block belongs on. Pins that a
	// blocked owner lists like any other and that --json omits the key.
	d := &blockedDirectory{repos: []coreapi.RepoIndexEntry{onboardedEntry("acme/web", "private", "us")}}
	d.serve(t)
	stdout, _ := runMirrorList(t)
	require.Regexp(t, `acme/web\s+`+defaultClusterHost+`\s+Private\s+ready\s`, stdout)
	require.NotContains(t, stdout, "blocked")
	stdout, _ = runMirrorList(t, "--json")
	require.NotContains(t, stdout, "syncBlock")
	require.Zero(t, d.reads())
}
