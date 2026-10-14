package review

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	reviewtypes "github.com/entireio/cli/cmd/entire/cli/review/types"
)

const (
	emptyTrailJSON  = `{"summary":"No issues","comments":[]}`
	oneHighComment  = `{"summary":"1 issue","comments":[{"severity":"high","body":"Loop ignores ctx cancel.","location":{"granularity":"line","file_path":"retry.go","start_line":42}}]}`
	oneMediumOnly   = `{"summary":"1 issue","comments":[{"severity":"medium","body":"Rename x.","location":{"granularity":"whole_change"}}]}`
	twoTrailComment = `{"summary":"2 issues","comments":[{"severity":"high","body":"A.","location":{"granularity":"line","file_path":"a.go","start_line":1}},{"severity":"low","body":"B.","location":{"granularity":"whole_change"}}]}`
)

type trailRun struct {
	name   string
	events []reviewtypes.Event
}

func textRun(name, narrative string) trailRun {
	return trailRun{name: name, events: []reviewtypes.Event{reviewtypes.AssistantText{Text: narrative}}}
}

func trailRunsSummary(runs ...trailRun) reviewtypes.RunSummary {
	summary := reviewtypes.RunSummary{}
	for _, run := range runs {
		summary.AgentRuns = append(summary.AgentRuns, reviewtypes.AgentRun{
			Name:   run.name,
			Status: reviewtypes.AgentStatusSucceeded,
			Buffer: run.events,
		})
	}
	return summary
}

// postedTrailReport runs the trail-mode posting decision and returns the
// report handed to the hook, whether the hook ran, and the printed output.
func postedTrailReport(t *testing.T, summary reviewtypes.RunSummary, aggregate string) (TrailReport, bool, string) {
	t.Helper()
	var out bytes.Buffer
	var got TrailReport
	called := false
	deps := Deps{PostReviewToTrail: func(_ context.Context, _ io.Writer, _ string, report TrailReport) error {
		called = true
		got = report
		return nil
	}}
	maybePostReviewToTrail(context.Background(), &out, deps, ReviewOutputTrail, "general", summary, aggregate)
	return got, called, out.String()
}

func requireNotPosted(t *testing.T, called bool, out string) {
	t.Helper()
	if called {
		t.Fatal("a clean review must not be posted to the trail")
	}
	if !strings.Contains(out, "Nothing to report") {
		t.Fatalf("expected a 'Nothing to report' notice, got %q", out)
	}
}

func requirePostedProse(t *testing.T, report TrailReport, called bool, wantSubstr string) {
	t.Helper()
	if !called {
		t.Fatal("expected the review to be posted")
	}
	if len(report.Comments) != 0 {
		t.Fatalf("expected a prose report, got %d structured comments", len(report.Comments))
	}
	if !strings.Contains(report.Prose, wantSubstr) {
		t.Fatalf("prose %q does not contain %q", report.Prose, wantSubstr)
	}
	if strings.Contains(report.Prose, `"comments"`) {
		t.Fatalf("the JSON contract line must be removed before posting prose, got %q", report.Prose)
	}
}

func TestTrailReportFromJudge(t *testing.T) {
	t.Parallel()
	reviewers := trailRunsSummary(
		textRun("claude-code", "approve — no actionable findings\n"+emptyTrailJSON),
		textRun("codex", "approve — looks good\n"+emptyTrailJSON),
	)

	t.Run("empty comments array is clean", func(t *testing.T) {
		t.Parallel()
		_, called, out := postedTrailReport(t, reviewers, "approve — no actionable findings\n"+emptyTrailJSON)
		requireNotPosted(t, called, out)
		if !strings.Contains(out, "0 comments") {
			t.Errorf("the notice should say why nothing was posted, got %q", out)
		}
	})

	t.Run("comments are posted as structured findings", func(t *testing.T) {
		t.Parallel()
		report, called, _ := postedTrailReport(t, reviewers, "request changes — two issues\n- [high] a.go:1 — A.\n- [low] B.\n"+twoTrailComment)
		if !called || len(report.Comments) != 2 {
			t.Fatalf("called=%v comments=%d, want 2 structured comments", called, len(report.Comments))
		}
		first := report.Comments[0]
		if first.Severity != "high" || first.Body != "A." || first.Location.FilePath != "a.go" || first.Location.StartLine != 1 {
			t.Errorf("first comment = %+v, want high A. at a.go:1", first)
		}
	})

	t.Run("prose without a JSON line is posted as before", func(t *testing.T) {
		t.Parallel()
		report, called, _ := postedTrailReport(t, reviewers, "approve — no actionable findings")
		requirePostedProse(t, report, called, "approve — no actionable findings")
	})

	t.Run("fenced JSON line is accepted", func(t *testing.T) {
		t.Parallel()
		_, called, out := postedTrailReport(t, reviewers, "approve — no actionable findings\n```json\n"+emptyTrailJSON+"\n```")
		requireNotPosted(t, called, out)
	})

	t.Run("pretty-printed JSON block is accepted", func(t *testing.T) {
		t.Parallel()
		_, called, out := postedTrailReport(t, reviewers, "approve — no actionable findings\n{\n  \"summary\": \"No issues\",\n  \"comments\": []\n}")
		requireNotPosted(t, called, out)
	})

	t.Run("null comments is not clean", func(t *testing.T) {
		t.Parallel()
		report, called, _ := postedTrailReport(t, reviewers, "approve — fine\n"+`{"summary":"","comments":null}`)
		requirePostedProse(t, report, called, "approve — fine")
	})

	t.Run("missing comments key is not clean", func(t *testing.T) {
		t.Parallel()
		report, called, _ := postedTrailReport(t, reviewers, "approve — fine\n"+`{"summary":"approve, no \"comments\""}`)
		requirePostedProse(t, report, called, "approve — fine")
	})

	t.Run("a comment with an empty body invalidates the JSON", func(t *testing.T) {
		t.Parallel()
		report, called, _ := postedTrailReport(t, reviewers, "approve — fine\n"+`{"summary":"","comments":[{"severity":"high","body":"  "}]}`)
		requirePostedProse(t, report, called, "approve — fine")
	})

	t.Run("empty JSON with a finding bullet in the prose is posted", func(t *testing.T) {
		t.Parallel()
		report, called, _ := postedTrailReport(t, reviewers, "approve\n- the retry loop leaks a goroutine\n"+emptyTrailJSON)
		requirePostedProse(t, report, called, "the retry loop leaks a goroutine")
	})

	t.Run("empty JSON with an indented finding bullet is posted", func(t *testing.T) {
		t.Parallel()
		report, called, _ := postedTrailReport(t, reviewers, "approve\n   * [medium] cmd.go:10 — leak\n"+emptyTrailJSON)
		requirePostedProse(t, report, called, "leak")
	})

	t.Run("empty JSON with a request changes verdict is posted", func(t *testing.T) {
		t.Parallel()
		report, called, _ := postedTrailReport(t, reviewers, "**Request changes** — the lock leaks on error\n"+emptyTrailJSON)
		requirePostedProse(t, report, called, "the lock leaks on error")
	})

	t.Run("empty JSON with an approve with nits verdict is posted", func(t *testing.T) {
		t.Parallel()
		report, called, _ := postedTrailReport(t, reviewers, "approve with nits — rename x\n"+emptyTrailJSON)
		requirePostedProse(t, report, called, "rename x")
	})
}

func TestTrailReportJudgeCannotEraseHighSeverityReviewerFindings(t *testing.T) {
	t.Parallel()

	t.Run("reviewer high comment survives an empty judge", func(t *testing.T) {
		t.Parallel()
		summary := trailRunsSummary(
			textRun("claude-code", "request changes\n- [high] retry.go:42 — Loop ignores ctx cancel.\n"+oneHighComment),
			textRun("codex", "approve\n"+emptyTrailJSON),
		)
		report, called, _ := postedTrailReport(t, summary, "approve — no actionable findings\n"+emptyTrailJSON)
		if !called || len(report.Comments) != 1 || report.Comments[0].Body != "Loop ignores ctx cancel." {
			t.Fatalf("called=%v report=%+v, want the reviewer's high comment posted", called, report)
		}
	})

	t.Run("reviewer medium comment defers to the judge", func(t *testing.T) {
		t.Parallel()
		summary := trailRunsSummary(
			textRun("claude-code", "approve with nits\n- [medium] Rename x.\n"+oneMediumOnly),
			textRun("codex", "approve\n"+emptyTrailJSON),
		)
		_, called, out := postedTrailReport(t, summary, "approve — no actionable findings\n"+emptyTrailJSON)
		requireNotPosted(t, called, out)
	})
}

func TestTrailReportWithoutJudge(t *testing.T) {
	t.Parallel()

	t.Run("single clean reviewer is not posted", func(t *testing.T) {
		t.Parallel()
		_, called, out := postedTrailReport(t, trailRunsSummary(textRun("claude-code", "approve — no actionable findings\n"+emptyTrailJSON)), "")
		requireNotPosted(t, called, out)
	})

	t.Run("each reviewer's JSON is read, not only the last one", func(t *testing.T) {
		t.Parallel()
		summary := trailRunsSummary(
			textRun("claude-code", "request changes\n- [high] retry.go:42 — Loop ignores ctx cancel.\n"+oneHighComment),
			textRun("codex", "approve — no actionable findings\n"+emptyTrailJSON),
		)
		report, called, _ := postedTrailReport(t, summary, "")
		if !called || len(report.Comments) != 1 || report.Comments[0].Location.FilePath != "retry.go" {
			t.Fatalf("called=%v report=%+v, want the first reviewer's comment posted", called, report)
		}
	})

	t.Run("all reviewers clean is not posted", func(t *testing.T) {
		t.Parallel()
		summary := trailRunsSummary(
			textRun("claude-code", "approve\n"+emptyTrailJSON),
			textRun("codex", "approve\n"+emptyTrailJSON),
		)
		_, called, out := postedTrailReport(t, summary, "")
		requireNotPosted(t, called, out)
	})

	t.Run("a reviewer without JSON posts the combined prose", func(t *testing.T) {
		t.Parallel()
		summary := trailRunsSummary(
			textRun("claude-code", "approve\n"+emptyTrailJSON),
			textRun("codex", "approve — looks fine"),
		)
		report, called, _ := postedTrailReport(t, summary, "")
		requirePostedProse(t, report, called, "## codex")
	})

	t.Run("streamed text deltas join into one parsable JSON line", func(t *testing.T) {
		t.Parallel()
		run := trailRun{name: "pi", events: []reviewtypes.Event{
			reviewtypes.AssistantText{Text: "approve — no actionable findings\n", Delta: true},
			reviewtypes.AssistantText{Text: `{"summary":"No issues",`, Delta: true},
			reviewtypes.AssistantText{Text: `"comments":[]}`, Delta: true},
		}}
		_, called, out := postedTrailReport(t, trailRunsSummary(run), "")
		requireNotPosted(t, called, out)
	})
}

func TestTrailFindingsJSONInstructionsExampleParses(t *testing.T) {
	t.Parallel()
	var example string
	for _, line := range strings.Split(trailFindingsJSONInstructions, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "{") {
			example = line
		}
	}
	if example == "" {
		t.Fatalf("instructions carry no JSON example:\n%s", trailFindingsJSONInstructions)
	}
	_, comments, ok := splitTrailJSON("approve\n" + example)
	if !ok || len(comments) != 1 {
		t.Fatalf("the prompt's own example must parse as one comment: ok=%v comments=%d\n%s", ok, len(comments), example)
	}
}

func TestStripTrailJSONForDisplay(t *testing.T) {
	t.Parallel()
	got := stripTrailJSON("approve — no actionable findings\n" + emptyTrailJSON)
	if got != "approve — no actionable findings" {
		t.Errorf("stripTrailJSON = %q, want the verdict line only", got)
	}
	if prose := "plain review text"; stripTrailJSON(prose) != prose {
		t.Errorf("text without a JSON line must be unchanged, got %q", stripTrailJSON(prose))
	}
}
