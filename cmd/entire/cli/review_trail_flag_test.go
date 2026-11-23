package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/cmd/entire/cli/api"
	cliReview "github.com/entireio/cli/cmd/entire/cli/review"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

const reviewTrailTestHead = "0123456789abcdef0123456789abcdef01234567"

// setupReviewTrailFlag serves trail #42, whose only change is widget/7 on
// feature/work, to a clone of acme/widget, and reports head as the head the
// review starts on. It returns the number of findings batches posted.
// Not parallel: changes CWD and replaces client constructors.
func setupReviewTrailFlag(t *testing.T, head *string) *int {
	t.Helper()
	dir := t.TempDir()
	testutil.InitRepo(t, dir)
	testutil.RunGit(t, dir, "remote", "add", "origin", "git@github.com:acme/widget.git")
	t.Chdir(dir)
	setupProjectTrailTest(t, func(w http.ResponseWriter, r *http.Request) {
		if !serveWorkingProjectRead(t, w, r) {
			t.Errorf("unexpected project request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	batches := 0
	work := api.TrailResource{ID: projectTrailTestChange, Number: 7, Branch: "feature/work",
		Parent: &api.TrailParentReference{ID: projectTrailTestID, Number: 42, ProjectID: projectTrailTestProject,
			Host: "gh", Project: "acme", Path: projectTrailTestPath, Jurisdiction: "eu", PrimaryProcessingCell: "project-cell"}}
	setupWorkingRepoClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/repos/repo-id/changes":
			assert.NoError(t, json.NewEncoder(w).Encode(api.TrailListResponse{Trails: []api.TrailResource{work}}))
		case "GET /api/v1/repos/repo-id/changes/7":
			assert.NoError(t, json.NewEncoder(w).Encode(work))
		case "POST " + trailReviewTestStartPath:
			assert.NoError(t, json.NewEncoder(w).Encode(api.TrailReviewStartResponse{ReviewID: "rvw_1", TrailID: projectTrailTestChange, HeadSHA: head}))
		case "POST " + trailReviewTestCommentsPath:
			batches++
			var batch api.TrailReviewCommentBatchRequest
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&batch))
			results := make([]api.TrailReviewCommentBatchResult, 0, len(batch.Comments))
			for _, c := range batch.Comments {
				results = append(results, api.TrailReviewCommentBatchResult{ClientID: c.ClientID, Status: "created",
					Comment: &api.TrailReviewComment{ID: "cm_1", ReviewID: "rvw_1", Status: trailReviewStatusOpen}})
			}
			assert.NoError(t, json.NewEncoder(w).Encode(api.TrailReviewCommentBatchResponse{Results: results}))
		default:
			t.Errorf("unexpected repository request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	return &batches
}

func reviewTrailTestCommand(out *bytes.Buffer) *cobra.Command {
	cmd := &cobra.Command{Use: "review"}
	cmd.SetContext(context.Background())
	cmd.SetOut(out)
	cmd.SetErr(out)
	return cmd
}

// --trail resolves the trail by number or by branch before the review runs,
// then posts only when the trail's head is the commit that was reviewed:
// findings carry no commit, so a stale head would attach them to code the
// review never read. Not parallel: changes CWD and replaces client constructors.
func TestResolveReviewTrailPostsOnlyOnTheReviewedHead(t *testing.T) {
	head := reviewTrailTestHead
	other := "fedcba9876543210fedcba9876543210fedcba98"
	for _, tt := range []struct {
		name     string
		selector string
		head     *string
		reviewed string
		posted   bool
		wantErr  string
	}{
		{"number on its head", "42", &head, strings.ToUpper(head), true, ""},
		{"branch on its head", "feature/work", &head, head, true, ""},
		{"head moved on", "42", &other, head, false, "the trail's head is fedcba98, not the reviewed commit 01234567"},
		{"head not reported", "42", nil, head, false, "the trail did not report its head commit"},
		{"reviewed commit unknown", "42", &head, "", false, "the reviewed commit is unknown"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			batches := setupReviewTrailFlag(t, tt.head)
			var out bytes.Buffer
			cmd := reviewTrailTestCommand(&out)
			post, err := resolveReviewTrail(cmd, tt.selector)
			require.NoError(t, err)
			require.Contains(t, out.String(), "Findings will be posted to trail #42")
			require.Contains(t, out.String(), "feature/work")

			out.Reset()
			err = post(context.Background(), &out, "general", cliReview.TrailReport{Prose: "a finding", HeadSHA: tt.reviewed})
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
				require.Contains(t, out.String(), "Posted the review verdict to trail #42")
			}
			require.Equal(t, map[bool]int{true: 1, false: 0}[tt.posted], *batches)
		})
	}
}

// The findings go to the change announced before the review, even when the
// selector resolves to another one by the time they are posted: a trail with
// several changes here prefers the checked-out branch, which the user may
// have switched while the reviewers ran.
// Not parallel: changes CWD and replaces client constructors.
func TestResolveReviewTrailPostsOnlyToTheAnnouncedChange(t *testing.T) {
	dir := t.TempDir()
	testutil.InitRepo(t, dir)
	testutil.RunGit(t, dir, "remote", "add", "origin", "git@github.com:acme/widget.git")
	testutil.WriteFile(t, dir, "README.md", "widget")
	testutil.GitAdd(t, dir, "README.md")
	testutil.GitCommit(t, dir, "initial")
	testutil.RunGit(t, dir, "branch", "feature/other")
	testutil.RunGit(t, dir, "checkout", "-b", "feature/work")
	t.Chdir(dir)
	const otherChange = "01ARZ3NDEKTSV4RRFFQ69G5FAY"
	parent := projectTrailTestResource()
	parent.Changes = []api.ChangeSummary{
		{ID: projectTrailTestChange, RepositoryID: "repo-id", Repository: "widget", Number: 7, Title: "Work", Branch: "feature/work"},
		{ID: otherChange, RepositoryID: "repo-id", Repository: "widget", Number: 8, Title: "Other", Branch: "feature/other"},
	}
	setupProjectTrailTest(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/gh/acme/trails/42", projectTrailTestPath:
			assert.NoError(t, json.NewEncoder(w).Encode(parent))
		case projectTrailTestPath + "/changes/" + projectTrailTestChange:
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"id": projectTrailTestChange, "number": 7, "trailId": projectTrailTestID, "repositoryId": "repo-id", "branch": "feature/work", "status": "open"}))
		case projectTrailTestPath + "/changes/" + otherChange:
			assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{"id": otherChange, "number": 8, "trailId": projectTrailTestID, "repositoryId": "repo-id", "branch": "feature/other", "status": "open"}))
		default:
			t.Errorf("unexpected project request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	setupWorkingRepoClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("must not post: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	})

	var out bytes.Buffer
	post, err := resolveReviewTrail(reviewTrailTestCommand(&out), "42")
	require.NoError(t, err)
	require.Contains(t, out.String(), "feature/work")
	testutil.RunGit(t, dir, "checkout", "feature/other")
	err = post(context.Background(), &out, "general", cliReview.TrailReport{Prose: "a finding", HeadSHA: reviewTrailTestHead})
	require.ErrorContains(t, err, "--trail 42 now resolves to")
	require.ErrorContains(t, err, "so the findings were not posted")
}

// A branch with no trail fails at once, before any reviewer would run.
// Not parallel: changes CWD and replaces client constructors.
func TestResolveReviewTrailRejectsABranchWithoutATrail(t *testing.T) {
	batches := setupReviewTrailFlag(t, nil)
	var out bytes.Buffer
	_, err := resolveReviewTrail(reviewTrailTestCommand(&out), "feature/none")
	require.ErrorContains(t, err, "feature/none")
	require.Zero(t, *batches)
}

func TestReviewTrailSelectorIsBranch(t *testing.T) {
	t.Parallel()
	for selector, want := range map[string]bool{
		"42":                         false,
		projectTrailTestID:           false,
		"cli/12":                     false,
		"feature/work":               true,
		"main":                       true,
		"claude/project-thread-abcd": true,
	} {
		assert.Equal(t, want, reviewTrailSelectorIsBranch(selector), selector)
	}
}

// A review's retry hint names the change with --trail, since review takes no
// trail positional argument.
func TestTrailChangeRetryCommandForReview(t *testing.T) {
	t.Parallel()
	root := &cobra.Command{Use: "entire"}
	root.AddCommand(cliReview.NewCommand(buildReviewDeps()))
	cmd, rest, err := root.Find([]string{"review", "general", "--timeout", "5m", "--trail", "42"})
	require.NoError(t, err)
	require.NoError(t, cmd.ParseFlags(rest))
	require.Equal(t, "entire review general --timeout=5m0s --trail=widget/7", trailChangeRetryCommand(cmd, "42", "widget/7", "gh/acme"))
}
