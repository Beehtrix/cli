package review_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/review"
	reviewtypes "github.com/entireio/cli/cmd/entire/cli/review/types"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

// trailFlagRun is one `entire review --trail` invocation against a stub
// reviewer that reports one finding.
type trailFlagRun struct {
	events    []reviewtypes.Event // nil reports one finding
	resolved  []string
	posted    *review.TrailReport
	started   bool
	out       string
	err       error
	reviewCfg reviewtypes.RunConfig
}

type trailFlagReviewer struct {
	run *trailFlagRun
}

func (r trailFlagReviewer) Name() string { return "claude-code" }
func (r trailFlagReviewer) Start(_ context.Context, cfg reviewtypes.RunConfig) (reviewtypes.Process, error) {
	r.run.started = true
	r.run.reviewCfg = cfg
	events := r.run.events
	if events == nil {
		events = []reviewtypes.Event{
			reviewtypes.Started{},
			reviewtypes.AssistantText{Text: "- [high] f.txt:1 a real bug"},
			reviewtypes.Finished{Success: true},
		}
	}
	return &scriptedDispatchProcess{events: events}, nil
}

// runTrailFlagReview runs `entire review <args>` in the current repository with
// a single claude-code reviewer. resolveErr fails --trail resolution; postErr
// fails posting.
func runTrailFlagReview(t *testing.T, resolveErr, postErr error, args ...string) *trailFlagRun {
	t.Helper()
	return runTrailFlagReviewWith(t, &trailFlagRun{}, resolveErr, postErr, args...)
}

func runTrailFlagReviewWith(t *testing.T, run *trailFlagRun, resolveErr, postErr error, args ...string) *trailFlagRun {
	t.Helper()
	deps := review.Deps{
		GetAgentsWithHooksInstalled: func(context.Context) []types.AgentName {
			return []types.AgentName{"claude-code"}
		},
		NewSilentError:          func(err error) error { return err },
		HeadHasReviewCheckpoint: func(context.Context) (bool, string) { return false, "" },
		ReviewerFor: func(string) reviewtypes.AgentReviewer {
			return trailFlagReviewer{run: run}
		},
		PostReviewToTrail: func(context.Context, io.Writer, string, review.TrailReport) error {
			t.Error("--trail must not post to the current branch's trail")
			return nil
		},
		ResolveTrail: func(_ *cobra.Command, selector string) (review.TrailPoster, error) {
			run.resolved = append(run.resolved, selector)
			if run.started {
				t.Error("--trail was resolved after the reviewer started")
			}
			if resolveErr != nil {
				return nil, resolveErr
			}
			return func(_ context.Context, w io.Writer, _ string, report review.TrailReport) error {
				run.posted = &report
				if postErr != nil {
					return postErr
				}
				_, err := io.WriteString(w, "Posted the review verdict to trail #42.\n")
				return err
			}, nil
		},
	}
	out := &bytes.Buffer{}
	cmd := review.NewCommand(deps)
	cmd.SetOut(out)
	cmd.SetErr(out)
	cmd.SetArgs(args)
	run.err = cmd.Execute()
	run.out = out.String()
	return run
}

// --trail posts to the named trail even when the profile keeps reviews local
// and the checkout is detached, hands the poster the reviewed commit, and
// resolves the trail before any reviewer runs. Not parallel: changes CWD.
func TestRunReview_TrailFlagPostsToNamedTrail(t *testing.T) {
	setupCmdTestRepo(t)
	if err := seedReviewConfig(context.Background(), map[string]settings.ReviewConfig{"claude-code": {Prompt: "review"}}); err != nil {
		t.Fatal(err)
	}
	head := strings.TrimSpace(testutil.RunGit(t, ".", "rev-parse", "HEAD"))
	testutil.RunGit(t, ".", "checkout", "--detach", "HEAD")

	run := runTrailFlagReview(t, nil, nil, "general", "--trail", " 42 ")
	if run.err != nil {
		t.Fatalf("review: %v\n%s", run.err, run.out)
	}
	if len(run.resolved) != 1 || run.resolved[0] != "42" {
		t.Fatalf("resolved %q, want one trimmed selector 42", run.resolved)
	}
	if run.posted == nil {
		t.Fatalf("nothing was posted:\n%s", run.out)
	}
	if run.posted.HeadSHA != head {
		t.Errorf("posted HeadSHA = %q, want the reviewed commit %q", run.posted.HeadSHA, head)
	}
	if !run.reviewCfg.TrailFindingsJSON {
		t.Error("the reviewer was not asked for the trail findings line, though --trail posts its output")
	}
}

// A --trail run fails when its findings are not posted, unlike a profile's
// output: trail, so a script can tell a posted review from a local one; a
// selector that names no trail fails before the agents start.
func TestRunReview_TrailFlagFailsWhenNotPosted(t *testing.T) {
	setupCmdTestRepo(t)
	if err := seedReviewConfig(context.Background(), map[string]settings.ReviewConfig{"claude-code": {Prompt: "review"}}); err != nil {
		t.Fatal(err)
	}

	run := runTrailFlagReview(t, nil, errors.New("the trail's head is abc, not the reviewed commit"), "general", "--trail", "42")
	if run.err == nil {
		t.Fatalf("a --trail review whose post failed exited zero:\n%s", run.out)
	}
	if !strings.Contains(run.out, "Could not post the review to the trail: the trail's head is abc") {
		t.Errorf("output does not say why nothing was posted:\n%s", run.out)
	}

	run = runTrailFlagReview(t, errors.New("no trail #999"), nil, "general", "--trail", "999")
	if run.err == nil || !strings.Contains(run.err.Error(), "--trail 999: no trail #999") {
		t.Fatalf("err = %v, want the resolution error", run.err)
	}
	if run.started {
		t.Error("the reviewer started though --trail named no trail")
	}
}

// --trail only means something for a review run, and an empty value would
// silently fall back to the current branch.
func TestRunReview_TrailFlagRejectsModesWithoutAReview(t *testing.T) {
	setupCmdTestRepo(t)
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"--trail", "", "general"}, "--trail cannot be empty"},
		{[]string{"--trail", "42", "--findings"}, "--trail applies to a review run"},
		{[]string{"--trail", "42", "--list"}, "--trail applies to a review run"},
	} {
		run := runTrailFlagReview(t, nil, nil, tt.args...)
		if run.err == nil || !strings.Contains(run.err.Error(), tt.want) {
			t.Errorf("%v: err = %v, want %q", tt.args, run.err, tt.want)
		}
		if len(run.resolved) != 0 || run.started {
			t.Errorf("%v: resolved %v, started %v; want neither", tt.args, run.resolved, run.started)
		}
	}
}

// A --trail review refuses a worktree with uncommitted changes, which the
// reviewers would read but the reviewed commit does not contain, before it
// resolves the trail or starts a reviewer. Not parallel: changes CWD.
func TestRunReview_TrailFlagRefusesUncommittedChanges(t *testing.T) {
	setupCmdTestRepo(t)
	if err := seedReviewConfig(context.Background(), map[string]settings.ReviewConfig{"claude-code": {Prompt: "review"}}); err != nil {
		t.Fatal(err)
	}
	testutil.WriteFile(t, ".", "scratch.txt", "not committed")

	run := runTrailFlagReview(t, nil, nil, "general", "--trail", "42")
	if run.err == nil || !strings.Contains(run.err.Error(), "uncommitted changes (1 paths)") {
		t.Fatalf("err = %v, want the uncommitted-changes refusal", run.err)
	}
	if len(run.resolved) != 0 || run.started {
		t.Errorf("resolved %v, started %v; want neither", run.resolved, run.started)
	}
}

// A reviewer that says nothing at all is not a clean review: a --trail run
// fails instead of reporting nothing to post. Not parallel: changes CWD.
func TestRunReview_TrailFlagFailsOnAnEmptyReview(t *testing.T) {
	setupCmdTestRepo(t)
	if err := seedReviewConfig(context.Background(), map[string]settings.ReviewConfig{"claude-code": {Prompt: "review"}}); err != nil {
		t.Fatal(err)
	}
	run := runTrailFlagReviewWith(t, &trailFlagRun{events: []reviewtypes.Event{
		reviewtypes.Started{}, reviewtypes.Finished{Success: true},
	}}, nil, nil, "general", "--trail", "42")
	if run.err == nil || !strings.Contains(run.err.Error(), "the review produced no output") {
		t.Fatalf("err = %v, want the empty-review failure\n%s", run.err, run.out)
	}
	if run.posted != nil {
		t.Error("an empty review was posted")
	}
}
