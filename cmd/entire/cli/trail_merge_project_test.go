package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/cmd/entire/cli/api"
)

// projectMergeRepoServer serves the selected change's repository: its
// repo_id-addressed detail with mergeability, and the merge POST.
func projectMergeRepoServer(t *testing.T, mergeability string, posts *[]string) {
	t.Helper()
	const detail = "/api/v1/repos/repo-id/trails/7"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.URL.Path {
		case "GET " + detail:
			_, err := fmt.Fprintf(w, `{"id":%q,"number":7,"status":"open","branch":"feature/work","base":"main","mergeability":%s,"actions":{"merge_with_bypass":{"state":"enabled","reason":null}}}`, projectTrailTestChange, mergeability)
			assert.NoError(t, err)
		case "POST " + detail + "/merge":
			body, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			*posts = append(*posts, string(body))
			_, err = fmt.Fprint(w, `{"ok":true,"mergeCommitSha":"merge-example"}`)
			assert.NoError(t, err)
		default:
			// A failed command also re-probes enablement on the trail list.
			if r.URL.Path != "/api/v1/trails/gh/acme/widget" {
				t.Errorf("unexpected repository request: %s %s", r.Method, r.URL.Path)
			}
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	old := newTrailAPIClient
	newTrailAPIClient = func(context.Context, bool, string, string, string) (*api.Client, string, error) {
		return api.NewClientWithBaseURL("repo-token", server.URL), "repo-id", nil
	}
	t.Cleanup(func() { newTrailAPIClient = old })
}

// merge resolves a project trail to its one change, merges it through that
// change's repository number, and names it as <repo>/<number>: a bare number
// in a project hint would mean a different project trail.
// Not parallel: replaces client constructors.
func TestProjectTrailMergeMergesTheSelectedChange(t *testing.T) {
	setupProjectTrailTest(t, func(w http.ResponseWriter, r *http.Request) {
		if !serveWorkingProjectRead(t, w, r) {
			t.Errorf("unexpected project request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	var posts []string
	projectMergeRepoServer(t, trailMergeability(t), &posts)

	out, _, err := executeProjectTrailTest(t, "merge", "42", "--repo", "gh/acme/widget")
	require.NoError(t, err)
	require.Len(t, posts, 1)
	require.JSONEq(t, `{"expectedHeadSha":"`+trailMergeTestHead+`"}`, posts[0])
	require.Contains(t, out, "Change widget/7 of trail #42 (feature/work → main)")
	require.Contains(t, out, "Merged change widget/7 of trail #42 into main (merge-example)")
	require.NotContains(t, out, "trail #7")
}

// Not parallel: replaces client constructors.
func TestProjectTrailMergeHintsUseTheChangeSelector(t *testing.T) {
	setupProjectTrailTest(t, func(w http.ResponseWriter, r *http.Request) {
		if !serveWorkingProjectRead(t, w, r) {
			t.Errorf("unexpected project request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	var posts []string
	projectMergeRepoServer(t, trailMergeability(t, blocked, baseChecksRed), &posts)

	_, _, err := executeProjectTrailTest(t, "merge", "42", "--repo", "gh/acme/widget")
	require.Error(t, err)
	require.Empty(t, posts)
	require.Contains(t, err.Error(), "change widget/7 of trail #42 is not mergeable: 1 blocking gate failing")
	require.Contains(t, err.Error(), "rerun 'entire trail merge widget/7 --force'")
	require.NotContains(t, err.Error(), "merge 7 ")
	require.NotContains(t, err.Error(), "merge 42 ")
}

// Not parallel: replaces client constructors.
func TestProjectTrailMergeJSONNamesTheChange(t *testing.T) {
	setupProjectTrailTest(t, func(w http.ResponseWriter, r *http.Request) {
		if !serveWorkingProjectRead(t, w, r) {
			t.Errorf("unexpected project request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	var posts []string
	projectMergeRepoServer(t, trailMergeability(t), &posts)

	out, _, err := executeProjectTrailTest(t, "merge", "42", "--repo", "gh/acme/widget", "--dry-run", "--json")
	require.NoError(t, err)
	require.Empty(t, posts)
	var result trailMergeResultJSON
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(out)), &result))
	require.Equal(t, "widget/7", result.Change)
	require.Equal(t, 7, result.Number)
	require.True(t, result.Mergeable)
}

// merge changes the work it selects, so like approve it never picks a change
// because its branch is checked out.
func TestProjectTrailMergeIsAWriteCommand(t *testing.T) {
	t.Parallel()
	require.True(t, trailChangeWriteCommands["merge"])
}
