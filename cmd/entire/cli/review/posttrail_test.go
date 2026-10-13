package review

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	reviewtypes "github.com/entireio/cli/cmd/entire/cli/review/types"
)

type postTrailAlreadyPrintedError struct {
	err error
}

func (e postTrailAlreadyPrintedError) Error() string {
	return e.err.Error()
}

func (e postTrailAlreadyPrintedError) Unwrap() error {
	return e.err
}

func (e postTrailAlreadyPrintedError) AlreadyPrinted() bool {
	return true
}

func postTrailSummary(narrative string) reviewtypes.RunSummary {
	var buf []reviewtypes.Event
	if narrative != "" {
		buf = []reviewtypes.Event{reviewtypes.AssistantText{Text: narrative}}
	}
	return reviewtypes.RunSummary{
		AgentRuns: []reviewtypes.AgentRun{{
			Name:   "claude-code",
			Status: reviewtypes.AgentStatusSucceeded,
			Buffer: buf,
		}},
	}
}

func TestMaybePostReviewToTrail(t *testing.T) {
	t.Parallel()

	t.Run("local mode never posts and stays silent about the trail", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		called := false
		deps := Deps{PostReviewToTrail: func(context.Context, io.Writer, string, string) error {
			called = true
			return nil
		}}
		maybePostReviewToTrail(context.Background(), &out, deps, ReviewOutputLocal, "general", postTrailSummary("a finding"), "")
		if called {
			t.Error("local mode must not post to the trail")
		}
		if out.Len() != 0 {
			t.Errorf("local mode should print nothing about the trail, got %q", out.String())
		}
	})

	t.Run("trail mode with output posts the verdict via the hook", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		gotVerdict := ""
		deps := Deps{PostReviewToTrail: func(_ context.Context, w io.Writer, _, verdict string) error {
			gotVerdict = verdict
			fmt.Fprintln(w, "Posted the review verdict to trail #1 as a finding.")
			fmt.Fprintln(w, "View the trail: https://entire.io/gh/o/r/trails/1/b")
			return nil
		}}
		maybePostReviewToTrail(context.Background(), &out, deps, ReviewOutputTrail, "general", postTrailSummary("real finding"), "the verdict")
		if gotVerdict != "the verdict" {
			t.Errorf("verdict passed to hook = %q, want %q", gotVerdict, "the verdict")
		}
		if !strings.Contains(out.String(), "Posted the review verdict to trail #1") ||
			!strings.Contains(out.String(), "View the trail:") {
			t.Errorf("expected posted confirmation + link, got %q", out.String())
		}
	})

	t.Run("trail mode with nothing to report confirms and skips posting", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		called := false
		deps := Deps{PostReviewToTrail: func(context.Context, io.Writer, string, string) error {
			called = true
			return nil
		}}
		// Empty aggregate and a reviewer that produced no narrative => nothing to report.
		maybePostReviewToTrail(context.Background(), &out, deps, ReviewOutputTrail, "general", postTrailSummary(""), "")
		if called {
			t.Error("must not post when there is nothing to report")
		}
		if !strings.Contains(out.String(), "Nothing to report") {
			t.Errorf("expected a 'nothing to report' confirmation, got %q", out.String())
		}
	})

	t.Run("trail mode surfaces a posting error", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		deps := Deps{PostReviewToTrail: func(context.Context, io.Writer, string, string) error {
			return errors.New("boom")
		}}
		maybePostReviewToTrail(context.Background(), &out, deps, ReviewOutputTrail, "general", postTrailSummary("a finding"), "")
		if !strings.Contains(out.String(), "Could not post the review to the trail") {
			t.Errorf("expected an error confirmation, got %q", out.String())
		}
	})

	t.Run("trail mode does not double print already-rendered auth errors", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		deps := Deps{PostReviewToTrail: func(_ context.Context, w io.Writer, _, _ string) error {
			fmt.Fprintln(w, "Not logged in. Run 'entire login' to authenticate.")
			return postTrailAlreadyPrintedError{err: errors.New("not logged in")}
		}}
		maybePostReviewToTrail(context.Background(), &out, deps, ReviewOutputTrail, "general", postTrailSummary("a finding"), "")
		got := out.String()
		if strings.Count(got, "Not logged in") != 1 {
			t.Fatalf("login hint count in output = %d, want 1; output: %q", strings.Count(got, "Not logged in"), got)
		}
		if strings.Contains(got, "Could not post the review to the trail") {
			t.Fatalf("already-rendered auth error was double printed: %q", got)
		}
	})

	t.Run("cancelled run stays silent", func(t *testing.T) {
		t.Parallel()
		var out bytes.Buffer
		summary := postTrailSummary("a finding")
		summary.Cancelled = true
		maybePostReviewToTrail(context.Background(), &out, Deps{}, ReviewOutputTrail, "general", summary, "verdict")
		if out.Len() != 0 {
			t.Errorf("cancelled run should print nothing, got %q", out.String())
		}
	})
}

func postTrailRuns(narratives ...string) reviewtypes.RunSummary {
	summary := reviewtypes.RunSummary{}
	for i, narrative := range narratives {
		summary.AgentRuns = append(summary.AgentRuns, reviewtypes.AgentRun{
			Name:   fmt.Sprintf("reviewer-%d", i),
			Status: reviewtypes.AgentStatusSucceeded,
			Buffer: []reviewtypes.Event{reviewtypes.AssistantText{Text: narrative}},
		})
	}
	return summary
}

type postTrailVerdictCase struct {
	aggregate  string
	narratives []string
}

func runPostTrailVerdictCase(t *testing.T, tc postTrailVerdictCase) (posted bool, out string) {
	t.Helper()
	var buf bytes.Buffer
	deps := Deps{PostReviewToTrail: func(context.Context, io.Writer, string, string) error {
		posted = true
		return nil
	}}
	summary := postTrailRuns(tc.narratives...)
	if len(tc.narratives) == 0 {
		summary = postTrailSummary("reviewer narrative")
	}
	maybePostReviewToTrail(context.Background(), &buf, deps, ReviewOutputTrail, "general", summary, tc.aggregate)
	return posted, buf.String()
}

func TestMaybePostReviewToTrailSkipsCleanVerdicts(t *testing.T) {
	t.Parallel()

	tests := map[string]postTrailVerdictCase{
		"single verdict":         {aggregate: "APPROVE - no actionable findings."},
		"period separator":       {aggregate: "approve. no actionable findings."},
		"plain-language reason":  {aggregate: "approve with no actionable findings"},
		"bold verdict":           {aggregate: "**approve** — no actionable findings."},
		"punctuation boundary":   {aggregate: "approve! looks good."},
		"heading verdict":        {aggregate: "## approve"},
		"bare verdict":           {aggregate: "approve"},
		"approved verdict":       {aggregate: "Approved."},
		"approved with reason":   {aggregate: "approved - no actionable findings."},
		"nothing to report":      {aggregate: "approve with nothing to report"},
		"unseparated looks good": {aggregate: "approve looks good"},
		"unseparated nothing":    {aggregate: "approve nothing to report"},
		"reviewer preamble":      {narratives: []string{"I checked the scoped diff and all call sites.\napprove — no actionable defects found."}},
		"every reviewer clean": {narratives: []string{
			"approve - no actionable findings.",
			"## Summary\nLet me inspect the diff.\napprove — nothing to report.",
		}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			posted, out := runPostTrailVerdictCase(t, tc)
			if posted {
				t.Fatal("clean review must not be posted to the trail")
			}
			if !strings.Contains(out, "Nothing to report") {
				t.Errorf("expected a 'nothing to report' confirmation, got %q", out)
			}
		})
	}
}

func TestMaybePostReviewToTrailPostsNonCleanVerdicts(t *testing.T) {
	t.Parallel()

	tests := map[string]postTrailVerdictCase{
		"request changes":                        {aggregate: "REQUEST CHANGES - missing input validation."},
		"approve with nits":                      {aggregate: "APPROVE WITH NITS - rename the confusing variable."},
		"punctuated approve nits":                {aggregate: "APPROVE: WITH NITS - rename the confusing variable."},
		"approved with condition":                {aggregate: "APPROVED pending another review."},
		"prose finding before approve":           {aggregate: "The lock in retry.go:42 leaks on error; release it in a defer.\napprove"},
		"pointer in approve reason":              {aggregate: "approve - retry.go:42 still needs a defer."},
		"trailing conditional approve":           {aggregate: "request changes - retry.go:42 never stops on cancel.\n\nApprove once that is fixed."},
		"reworded nits":                          {aggregate: "approve with minor nits - rename x."},
		"parenthesized nits":                     {aggregate: "approve (nits: rename x)"},
		"qualified approve":                      {aggregate: "approve, but tighten the error in foo.go:12."},
		"conditional approve":                    {aggregate: "approve if retry.go:42 releases the lock on error."},
		"provided approve":                       {aggregate: "approve provided the retry loop stops on cancel."},
		"approve with suggestions":               {aggregate: "approve with suggestions - see retry.go:42."},
		"approve with notes":                     {aggregate: "approve with notes - rename the variable x"},
		"approve with nonblocking comments":      {aggregate: "approve with nonblocking comments: rename x"},
		"feedback as approve reason":             {aggregate: "approve - the retry loop never stops on cancel"},
		"suggestion as approve reason":           {aggregate: "approve - rename x to y for clarity"},
		"unseparated suggestion":                 {aggregate: "approve rename x to y for clarity"},
		"no-issue reason with feedback":          {aggregate: "approve - no tests cover the retry path"},
		"clean phrase then though":               {aggregate: "approve — looks good, though the lock leaks on error"},
		"finding before approve":                 {aggregate: "- [low] retry.go:42 — the lock leaks on error; release it in a defer.\n\napprove"},
		"marked finding before approve":          {aggregate: "[medium] retry.go:42 — the lock leaks on error.\napprove - otherwise fine."},
		"indented finding before approve":        {aggregate: "Checked the diff.\n  - [high] retry.go:42 — the lock leaks on error.\napprove"},
		"indented marked finding before approve": {aggregate: "Checked the diff.\n\t[high] retry.go:42 — the lock leaks on error.\napprove"},
		"reviewer finding before approve": {narratives: []string{
			"Let me inspect the diff.\n1. retry.go:42 never releases the lock on error.\napprove",
		}},
		"one reviewer requests": {narratives: []string{
			"request changes - foo.go:10 leaks the lock on error.",
			"approve - no actionable findings.",
		}},
		"one reviewer has no verdict": {narratives: []string{
			"The lock in foo.go:10 leaks on error.",
			"approve - no actionable findings.",
		}},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if posted, out := runPostTrailVerdictCase(t, tc); !posted {
				t.Fatalf("non-clean review must be posted to the trail, got %q", out)
			}
		})
	}
}
