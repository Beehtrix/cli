package cli

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/entireio/cli/internal/coreapi"
)

// initWhereamiRepo creates an isolated git repo, optionally with an origin
// remote, and chdirs into it. No t.Parallel in these tests: t.Chdir and
// withFakeCellCore both mutate process-global state.
func initWhereamiRepo(t *testing.T, originURL string) {
	t.Helper()
	dir := t.TempDir()
	runWhereamiGit(t, dir, "init", "-q")
	if originURL != "" {
		runWhereamiGit(t, dir, "remote", "add", "origin", originURL)
	}
	t.Chdir(dir)
}

func runWhereamiGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), "git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// runWhereami keeps stdout separate from stderr so the purity assertions
// below check the stream a script would actually consume.
func runWhereami(t *testing.T, args ...string) (stdout string, err error) {
	t.Helper()
	root := NewRootCmd()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(append([]string{"repo", "whereami"}, args...))
	err = root.Execute()
	return out.String(), err
}

func TestWhereamiJSONOutput_GitHubMirror_SelectsProcessingPlacement(t *testing.T) {
	initWhereamiRepo(t, "https://github.com/acme/widget.git")
	// The processing placement is deliberately NOT first: whereami must pick
	// it by id, exactly as resolveProcessingPlacement does.
	withFakeCellCore(t, &fakeCellCore{
		repos: reposOutput(repoIndexFixture("acme/widget", "mirror-eu",
			placementFixture{id: "mirror-us", slug: usClusterSlug},
			placementFixture{id: "mirror-eu", slug: euClusterSlug},
		)),
		clusters: clustersWithSlugs(),
	})

	stdout, err := runWhereami(t, "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := `{"repo_id":"mirror-eu","api_url":"` + euCellAPIURL + `","jurisdiction":"eu"}` + "\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want exactly %q", stdout, want)
	}
}

func TestWhereamiJSONOutput_NativeRepo(t *testing.T) {
	const (
		projectID = "01NATIVEPROJECT00000000000"
		nativeID  = "01NATIVEREPOSITORY00000000"
	)
	initWhereamiRepo(t, "entire://eu.entire.io/et/entirehq/marvin")
	withFakeCellCore(t, &fakeCellCore{
		resolution: nativeResolution("entirehq/marvin", nativeID),
		repo: &coreapi.Repo{
			ID: nativeID, Name: "marvin", OwningProjectId: projectID,
			ClusterHost: coreapi.NewOptString("eu.entire.io"),
		},
		clusters: euClusters(),
	})

	stdout, err := runWhereami(t, "--json")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	want := `{"repo_id":"` + nativeID + `","api_url":"` + euCellAPIURL + `","jurisdiction":"eu"}` + "\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want exactly %q", stdout, want)
	}
}

// Trail #1555 finding: resolveForgeRepoCellPlacement's errors already name
// the repo, so wrapping them here printed it twice.
func TestWhereamiPlacementErrorNamesRepoOnce(t *testing.T) {
	initWhereamiRepo(t, "https://gitlab.com/acme/widget.git")

	stdout, err := runWhereami(t, "--json")
	if err == nil {
		t.Fatal("expected an error for an unsupported forge")
	}
	if got := strings.Count(err.Error(), "acme/widget"); got != 1 {
		t.Fatalf("error names acme/widget %d times, want exactly once: %q", got, err)
	}
	if stdout != "" {
		t.Fatalf("stdout must stay empty on failure, got %q", stdout)
	}
}

func TestWhereamiNoOriginRemote(t *testing.T) {
	initWhereamiRepo(t, "")

	stdout, err := runWhereami(t, "--json")
	if err == nil {
		t.Fatal("expected an error for a repo without an origin remote")
	}
	if !strings.Contains(err.Error(), "origin") {
		t.Fatalf("error %q does not name the origin remote", err)
	}
	if stdout != "" {
		t.Fatalf("stdout must stay empty on failure, got %q", stdout)
	}
}

func TestWhereamiRequiresJSONFlag(t *testing.T) {
	// No origin remote: the flag check must come before any resolution.
	initWhereamiRepo(t, "")

	stdout, err := runWhereami(t)
	if err == nil || !strings.Contains(err.Error(), "only --json output is supported") {
		t.Fatalf("err = %v, want the --json refusal", err)
	}
	if stdout != "" {
		t.Fatalf("stdout must stay empty, got %q", stdout)
	}
}
