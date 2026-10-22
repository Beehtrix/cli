package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/internal/coreapi"
)

// fakeMirrorLister serves GET /mirrors the way Core does: the caller's whole
// visible set in a fixed order, cut into pages of pageSize BEFORE any filter
// is applied, so a page can come back short or empty with a cursor that says
// more remain. It records every call so a test can count pages and inspect
// the parameters sent.
type fakeMirrorLister struct {
	mu       sync.Mutex
	mirrors  []coreapi.Mirror
	err      error
	calls    []coreapi.ListMirrorsParams
	pageSize int // what the server caps a page at; 0 means honour the request
}

func (f *fakeMirrorLister) ListMirrors(_ context.Context, p coreapi.ListMirrorsParams) (*coreapi.ListMirrorsOutputBody, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, p)
	if f.err != nil {
		return nil, f.err
	}
	size := int(p.PageSize.Or(500))
	if f.pageSize > 0 && f.pageSize < size {
		size = f.pageSize
	}
	start := 0
	if tok := p.PageToken.Or(""); tok != "" {
		var err error
		if start, err = strconv.Atoi(tok); err != nil {
			return nil, fmt.Errorf("bad cursor %q", tok)
		}
	}
	end := min(start+size, len(f.mirrors))
	out := &coreapi.ListMirrorsOutputBody{}
	for _, m := range f.mirrors[start:end] {
		if o := p.Owner.Or(""); o != "" && !strings.EqualFold(o, m.Owner) {
			continue // filtered after the cut, as the server does
		}
		out.Mirrors = append(out.Mirrors, m)
	}
	if end < len(f.mirrors) {
		out.NextPageToken = coreapi.NewOptString(strconv.Itoa(end))
	}
	return out, nil
}

func (f *fakeMirrorLister) pages() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
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

func TestLookupSyncBlocks(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	wantBlock := &mirrorSyncBlock{Reason: syncBlockOrgIPAllowlist, ObservedAt: testBlockObservedAt}

	t.Run("a blocked owner behind another owner's mirrors is still found", func(t *testing.T) {
		t.Parallel()
		// The reviewer's reproduction: the caller's lowest-id mirror belongs
		// to someone else, so a one-entry page filtered to acme is empty.
		f := &fakeMirrorLister{mirrors: []coreapi.Mirror{
			mirrorOf("zzz-other", "x"),
			blockedMirrorOf("acme"),
		}, pageSize: 1}
		blocks, err := lookupSyncBlocks(ctx, f, []string{"acme"})
		require.NoError(t, err)
		require.Equal(t, wantBlock, blocks["acme"])
		require.Empty(t, f.calls[0].Owner.Or(""), "the walk sends no owner filter: the server filters after the cut")
		require.Equal(t, int32(syncBlockPageSize), f.calls[0].PageSize.Or(0), "ask for the biggest page the server allows")
	})

	t.Run("the walk stops as soon as every wanted owner has been seen", func(t *testing.T) {
		t.Parallel()
		f := &fakeMirrorLister{mirrors: []coreapi.Mirror{
			mirrorOf("other", "a"),
			blockedMirrorOf("acme"),
			mirrorOf("acme", "api"), // same installation; never needs reading
			mirrorOf("third", "z"),
		}, pageSize: 1}
		blocks, err := lookupSyncBlocks(ctx, f, []string{"acme", "other"})
		require.NoError(t, err)
		require.Equal(t, wantBlock, blocks["acme"])
		require.Nil(t, blocks["other"])
		require.Equal(t, 2, f.pages(), "two pages see both owners; the rest of the set is never fetched")
	})

	t.Run("an owner with no visible mirror walks to the end and reads unblocked", func(t *testing.T) {
		t.Parallel()
		f := &fakeMirrorLister{mirrors: []coreapi.Mirror{mirrorOf("other", "a"), mirrorOf("other", "b")}, pageSize: 1}
		blocks, err := lookupSyncBlocks(ctx, f, []string{"ghost"})
		require.NoError(t, err)
		require.Nil(t, blocks["ghost"])
		require.Equal(t, 2, f.pages())
	})

	t.Run("owner matching is case-insensitive, keyed as asked", func(t *testing.T) {
		t.Parallel()
		f := &fakeMirrorLister{mirrors: []coreapi.Mirror{blockedMirrorOf("Acme")}}
		blocks, err := lookupSyncBlocks(ctx, f, []string{"acme"})
		require.NoError(t, err)
		require.Equal(t, wantBlock, blocks["acme"])
	})

	t.Run("no owners means no request", func(t *testing.T) {
		t.Parallel()
		f := &fakeMirrorLister{err: errors.New("must not be called")}
		blocks, err := lookupSyncBlocks(ctx, f, nil)
		require.NoError(t, err)
		require.Empty(t, blocks)
		require.Zero(t, f.pages())
	})

	t.Run("a failed read is an error, not an absent block", func(t *testing.T) {
		t.Parallel()
		f := &fakeMirrorLister{err: errors.New("boom")}
		_, err := lookupSyncBlocks(ctx, f, []string{"acme"})
		require.EqualError(t, err, "read mirrors: boom")
	})
}

func TestAnnotateSyncBlocks(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	placed := []repoDirPlacement{{Cluster: defaultClusterHost, Status: "ready"}}

	t.Run("every mirrored row of a blocked owner is stamped; candidates and native rows are not", func(t *testing.T) {
		t.Parallel()
		f := &fakeMirrorLister{mirrors: []coreapi.Mirror{
			blockedMirrorOf("acme"),
			mirrorOf("other", "tool"),
		}}
		rows := []repoDirRow{
			{Repo: "/gh/acme/web", Status: "ready", Placements: placed},
			{Repo: "/gh/acme/api", Status: "ready", Placements: placed},
			{Repo: "/gh/other/tool", Status: "ready", Placements: placed},
			{Repo: "/gh/acme/marketing", Status: "available", Access: "admin"}, // candidate: nothing to sync
			{Repo: "/et/acme/native", Status: "ready", Placements: placed},     // native: no installation
		}
		require.NoError(t, annotateSyncBlocks(ctx, f, rows))
		require.Equal(t, 1, f.pages(), "one walk serves every owner")
		require.NotNil(t, rows[0].SyncBlock)
		require.NotNil(t, rows[1].SyncBlock)
		require.Equal(t, syncBlockOrgIPAllowlist, rows[1].SyncBlock.Reason)
		require.Nil(t, rows[2].SyncBlock)
		require.Nil(t, rows[3].SyncBlock)
		require.Nil(t, rows[4].SyncBlock)
	})

	t.Run("no mirrored GitHub rows means no request at all", func(t *testing.T) {
		t.Parallel()
		f := &fakeMirrorLister{err: errors.New("must not be called")}
		rows := []repoDirRow{
			{Repo: "/gh/acme/marketing", Status: "available"},
			{Repo: "/et/acme/native", Status: "ready", Placements: placed},
		}
		require.NoError(t, annotateSyncBlocks(ctx, f, rows))
		require.Zero(t, f.pages())
	})

	t.Run("a failed lookup leaves the rows unstamped", func(t *testing.T) {
		t.Parallel()
		f := &fakeMirrorLister{err: errors.New("boom")}
		rows := []repoDirRow{{Repo: "/gh/acme/web", Status: "ready", Placements: placed}}
		require.Error(t, annotateSyncBlocks(ctx, f, rows))
		require.Nil(t, rows[0].SyncBlock)
	})
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
// acme is blocked. It serves /repos, /clusters and /mirrors — the last with
// Core's filter-after-cut paging over a set whose first mirror belongs to
// someone else, so a lookup that did not walk would miss the block.
type blockedDirectory struct {
	repos         []coreapi.RepoIndexEntry
	mirrorsStatus int // >= 400 fails the lookup with a problem
	onMirrors     func()
	mirrorReads   int
	mu            sync.Mutex
}

func (d *blockedDirectory) serve(t *testing.T) {
	t.Helper()
	clusters := []coreapi.Cluster{{Slug: "us", PublicUrl: "https://" + defaultClusterHost}}
	lister := &fakeMirrorLister{mirrors: []coreapi.Mirror{
		mirrorOf("zzz-other", "x"),
		blockedMirrorOf("acme"),
		mirrorOf("other", "tool"),
	}, pageSize: 1}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case testClustersPath:
			writeJSONResponse(t, w, http.StatusOK, &coreapi.ListClustersOutputBody{Clusters: clusters})
		case testReposPath:
			out := &coreapi.ListReposOutputBody{Repos: d.repos}
			if size := r.URL.Query().Get("pageSize"); size != "" {
				out.NextPageToken = coreapi.NewOptString("more")
			}
			writeJSONResponse(t, w, http.StatusOK, out)
		case testMirrorsPath:
			d.mu.Lock()
			d.mirrorReads++
			d.mu.Unlock()
			if d.onMirrors != nil {
				d.onMirrors()
			}
			if d.mirrorsStatus >= 400 {
				writeCoreProblem(t, w, d.mirrorsStatus, "mirrors unavailable")
				return
			}
			var params coreapi.ListMirrorsParams
			params.Owner = coreapi.NewOptString(r.URL.Query().Get("owner"))
			if tok := r.URL.Query().Get("pageToken"); tok != "" {
				params.PageToken = coreapi.NewOptString(tok)
			}
			if size := r.URL.Query().Get("pageSize"); size != "" {
				n, err := strconv.Atoi(size)
				assert.NoError(t, err)
				params.PageSize = coreapi.NewOptInt32(int32(n))
			}
			out, err := lister.ListMirrors(r.Context(), params)
			assert.NoError(t, err)
			writeJSONResponse(t, w, http.StatusOK, out)
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
	return d.mirrorReads
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
		(&blockedDirectory{repos: web, mirrorsStatus: http.StatusServiceUnavailable}).serve(t)
		cmd := newRepoCmd()
		var out, errW strings.Builder
		cmd.SetOut(&out)
		cmd.SetErr(&errW)
		cmd.SetArgs([]string{"view", "/gh/acme/web"})
		require.NoError(t, cmd.ExecuteContext(t.Context()))
		require.Contains(t, out.String(), "ready")
		require.NotContains(t, out.String(), "Sync:")
		require.Equal(t, "Note: GitHub sync status unavailable: mirrors unavailable\n", errW.String())
	})

	t.Run("an interruption during the lookup fails the view", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		(&blockedDirectory{repos: web, onMirrors: cancel}).serve(t)
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
func TestRepoMirrorList_SyncBlock(t *testing.T) {
	repos := []coreapi.RepoIndexEntry{
		onboardedEntry("acme/web", "private", "us"),
		onboardedEntry("other/tool", "private", "us"),
		candidateEntry("acme/marketing", "public", coreapi.RepoCandidateAccessAdmin, true),
	}

	t.Run("a blocked owner's rows qualify the STATUS cell; --status still matches the lifecycle word", func(t *testing.T) {
		d := &blockedDirectory{repos: repos}
		d.serve(t)
		stdout, _ := runMirrorList(t)
		require.Regexp(t, `acme/web\s+`+defaultClusterHost+`\s+Private\s+ready \(sync blocked\)`, stdout)
		require.Regexp(t, `other/tool\s+`+defaultClusterHost+`\s+Private\s+ready\s`, stdout)
		require.NotRegexp(t, `acme/marketing[^\n]*sync blocked`, stdout, "a candidate has nothing to sync")
		require.Equal(t, 3, d.reads(), "one walk; other is the third mirror, so three pages see both owners")

		stdout, _ = runMirrorList(t, "--status", "ready")
		require.Contains(t, stdout, "acme/web")
	})

	t.Run("--json carries the block as its own field, status untouched", func(t *testing.T) {
		(&blockedDirectory{repos: repos}).serve(t)
		stdout, _ := runMirrorList(t, "--json")
		var rows []repoDirRow
		require.NoError(t, json.Unmarshal([]byte(stdout), &rows))
		byRepo := make(map[string]repoDirRow, len(rows))
		for _, r := range rows {
			byRepo[r.Repo] = r
		}
		require.Equal(t, "ready", byRepo["/gh/acme/web"].Status)
		require.Equal(t, &mirrorSyncBlock{Reason: syncBlockOrgIPAllowlist, ObservedAt: testBlockObservedAt}, byRepo["/gh/acme/web"].SyncBlock)
		require.Nil(t, byRepo["/gh/other/tool"].SyncBlock)
		require.Nil(t, byRepo["/gh/acme/marketing"].SyncBlock)
	})

	t.Run("--limit annotates only the rows shown", func(t *testing.T) {
		d := &blockedDirectory{repos: repos}
		d.serve(t)
		stdout, _ := runMirrorList(t, "--limit", "1", "--sort", "-name")
		require.Contains(t, stdout, "other/tool")
		require.NotContains(t, stdout, "acme/web")
		require.Equal(t, 3, d.reads(), "other sits last in the mirror set, so its walk runs to the end — but acme was never asked about")
	})

	t.Run("page mode annotates the page it shows", func(t *testing.T) {
		(&blockedDirectory{repos: repos}).serve(t)
		stdout, _ := runMirrorList(t, "--page-size", "10")
		require.Regexp(t, `acme/web[^\n]*ready \(sync blocked\)`, stdout)
	})

	t.Run("--forge et makes no lookup", func(t *testing.T) {
		d := &blockedDirectory{repos: []coreapi.RepoIndexEntry{nativeEntry("acme/native", "private", "us")}}
		d.serve(t)
		stdout, _ := runMirrorList(t, "--forge", nativeCloneForge)
		require.Contains(t, stdout, "/et/acme/native")
		require.Zero(t, d.reads())
	})

	t.Run("a failed lookup is a note, and the table still lists", func(t *testing.T) {
		(&blockedDirectory{repos: repos, mirrorsStatus: http.StatusServiceUnavailable}).serve(t)
		stdout, stderr := runMirrorList(t)
		require.Contains(t, stdout, "acme/web")
		require.NotContains(t, stdout, "sync blocked")
		require.Contains(t, stderr, "Note: GitHub sync status unavailable: mirrors unavailable")
	})

	t.Run("an interruption during the lookup fails the listing", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		(&blockedDirectory{repos: repos, onMirrors: cancel}).serve(t)
		parent := newRepoMirrorCmd()
		addControlPlaneFlags(parent)
		var out, errW strings.Builder
		parent.SetOut(&out)
		parent.SetErr(&errW)
		parent.SetArgs([]string{"list", "--no-pager"})
		err := parent.ExecuteContext(ctx)
		require.ErrorIs(t, err, context.Canceled)
		require.NotContains(t, errW.String(), "sync status unavailable")
	})
}
