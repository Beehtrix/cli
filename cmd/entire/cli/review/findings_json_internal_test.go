package review

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	reviewtypes "github.com/entireio/cli/cmd/entire/cli/review/types"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

const findingsJSONTestHandle = "claude-session"

// The manifest keeps what a trail post would read from the whole run, and
// why a run left no report, so --findings --json can tell a review that
// found nothing from one that left nothing to read.
func TestBuildLocalReviewManifestFromSummary_KeepsTrailReport(t *testing.T) {
	t.Parallel()
	failed := reviewtypes.RunSummary{AgentRuns: []reviewtypes.AgentRun{{Name: "claude-code", Status: reviewtypes.AgentStatusFailed}}}
	tests := []struct {
		name      string
		summary   reviewtypes.RunSummary
		aggregate string
		want      ManifestReport
	}{
		{
			name:    "structured findings",
			summary: trailRunsSummary(textRun(manifestTestCodexAgent, "[high] Loop ignores ctx cancel.\n"+oneHighComment)),
			want: ManifestReport{Comments: []TrailComment{{
				Severity: "high",
				Body:     "Loop ignores ctx cancel.",
				Location: TrailLocation{Granularity: "line", FilePath: "retry.go", StartLine: 42},
			}}},
		},
		{
			name:    "nothing to report",
			summary: trailRunsSummary(textRun(manifestTestCodexAgent, "approve\n"+emptyTrailJSON)),
			want:    ManifestReport{Comments: []TrailComment{}},
		},
		{
			name:    "prose without a contract",
			summary: trailRunsSummary(textRun(manifestTestCodexAgent, "- [high] retry.go:42 — loop ignores ctx cancel")),
			want:    ManifestReport{Comments: []TrailComment{}, Prose: "## codex\n\n- [high] retry.go:42 — loop ignores ctx cancel"},
		},
		{
			name:    "no output",
			summary: failed,
			want:    ManifestReport{Comments: []TrailComment{}, Unreadable: "the review produced no output"},
		},
		{
			name:      "no readable report",
			summary:   failed,
			aggregate: `{"comments":"none"}`,
			want:      ManifestReport{Comments: []TrailComment{}, Unreadable: "the review produced no readable report"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			manifest := buildLocalReviewManifestFromSummary("/repo", "abc123", tt.summary, nil, tt.aggregate)
			if manifest.Report == nil {
				t.Fatal("manifest kept no report")
			}
			if !reflect.DeepEqual(*manifest.Report, tt.want) {
				t.Fatalf("report = %#v, want %#v", *manifest.Report, tt.want)
			}
		})
	}
}

// writeFindingsJSONTestManifest saves one review run with report in a new
// repository and changes into it. askedForTrailJSON is whether the run asked
// for the trail JSON line, as a trail-mode run does.
func writeFindingsJSONTestManifest(t *testing.T, report *ManifestReport, askedForTrailJSON bool) {
	t.Helper()
	tmp := t.TempDir()
	testutil.InitRepo(t, tmp)
	testutil.WriteFile(t, tmp, "f.txt", "init")
	testutil.GitAdd(t, tmp, "f.txt")
	testutil.GitCommit(t, tmp, "init")
	t.Chdir(tmp)
	if err := writeLocalReviewManifest(context.Background(), LocalReviewManifest{
		CreatedAt:         time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC),
		StartingSHA:       "abc123",
		Sources:           []ManifestSource{{SessionID: findingsJSONTestHandle, Label: "Claude Code", Output: manifestTestFinding}},
		Report:            report,
		TrailFindingsJSON: askedForTrailJSON,
	}); err != nil {
		t.Fatalf("writeLocalReviewManifest: %v", err)
	}
}

func runReviewCommand(deps Deps, args ...string) (stdout, stderr string, err error) {
	cmd := NewCommand(deps)
	var out, errOut strings.Builder
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func TestReviewFindingsCommand_JSONPrintsTheSavedReportsFindings(t *testing.T) {
	saved := &ManifestReport{
		Comments: []TrailComment{{Severity: "high", Body: "Loop ignores ctx cancel.", Location: TrailLocation{Granularity: "line", FilePath: "retry.go", StartLine: 42}}},
		Prose:    "- nit: rename n",
	}
	writeFindingsJSONTestManifest(t, saved, true)
	confidence := 0.9
	want := []Finding{{Severity: "high", Confidence: &confidence, File: "retry.go", Line: 42, Body: "Loop ignores ctx cancel."}}
	var split TrailReport
	deps := Deps{TrailFindings: func(report TrailReport) []Finding {
		split = report
		return want
	}}

	stdout, stderr, err := runReviewCommand(deps, manifestFindingsFlag, findingsJSONTestHandle, "--json")
	if err != nil {
		t.Fatalf("review --findings --json: %v\nstderr:\n%s", err, stderr)
	}
	if !reflect.DeepEqual(split, TrailReport{Comments: saved.Comments, Prose: saved.Prose}) {
		t.Fatalf("split report = %#v, want the saved one", split)
	}
	var doc reviewFindingsJSON
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, stdout)
	}
	wantDoc := reviewFindingsJSON{
		Handle:    "20260507T100000",
		CreatedAt: time.Date(2026, 5, 7, 10, 0, 0, 0, time.UTC),
		HeadSHA:   "abc123",
		Findings:  want,
	}
	if !reflect.DeepEqual(doc, wantDoc) {
		t.Fatalf("printed %#v, want %#v", doc, wantDoc)
	}
}

// A run with nothing to report prints an empty list, not null, so a script
// can range over it.
func TestReviewFindingsCommand_JSONPrintsAnEmptyListForACleanReview(t *testing.T) {
	writeFindingsJSONTestManifest(t, &ManifestReport{Comments: []TrailComment{}}, true)
	deps := Deps{TrailFindings: func(TrailReport) []Finding { return nil }}

	stdout, stderr, err := runReviewCommand(deps, manifestFindingsFlag, findingsJSONTestHandle, "--json")
	if err != nil {
		t.Fatalf("review --findings --json: %v\nstderr:\n%s", err, stderr)
	}
	if !strings.Contains(stdout, `"findings": []`) {
		t.Fatalf("stdout should hold an empty findings list:\n%s", stdout)
	}
}

// A run that did not ask for the trail JSON line, such as a local profile's,
// has no line to say it is clean: prose with none of the finding shapes the
// output format asks for prints an empty list, and any shape still prints
// as findings. A trail-mode run's prose is a finding, as posting treats it.
func TestReviewFindingsCommand_JSONReadsACleanVerdictWithoutTheTrailLine(t *testing.T) {
	tests := []struct {
		name              string
		prose             string
		askedForTrailJSON bool
		wantSplit         bool
	}{
		{"local verdict only", "## codex\n\napprove — no actionable findings", false, false},
		{"local judge verdict", "Verdict: approve. The change is small and tested.", false, false},
		{"local finding", "## codex\n\nrequest changes\n\n- [high] retry.go:42 — loop ignores ctx cancel", false, true},
		{"local feedback verdict without a bullet", "request changes: the retry loop ignores ctx cancel", false, true},
		{"trail run without its line", "## codex\n\napprove — no actionable findings", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeFindingsJSONTestManifest(t, &ManifestReport{Comments: []TrailComment{}, Prose: tt.prose}, tt.askedForTrailJSON)
			split := false
			deps := Deps{TrailFindings: func(TrailReport) []Finding {
				split = true
				return []Finding{{Body: "split"}}
			}}

			stdout, stderr, err := runReviewCommand(deps, manifestFindingsFlag, findingsJSONTestHandle, "--json")
			if err != nil {
				t.Fatalf("review --findings --json: %v\nstderr:\n%s", err, stderr)
			}
			if split != tt.wantSplit {
				t.Fatalf("report split = %v, want %v\nstdout:\n%s", split, tt.wantSplit, stdout)
			}
			if !tt.wantSplit && !strings.Contains(stdout, `"findings": []`) {
				t.Fatalf("stdout should hold an empty findings list:\n%s", stdout)
			}
		})
	}
}

// A run that left no report, or was saved before its report was kept, fails
// with nothing on stdout, so a script never reads it as a clean review.
func TestReviewFindingsCommand_JSONRefusesARunWithoutAReport(t *testing.T) {
	tests := []struct {
		name   string
		report *ManifestReport
		want   string
	}{
		{"saved before reports were kept", nil, "saved before --json could read them"},
		{"no output", &ManifestReport{Comments: []TrailComment{}, Unreadable: "the review produced no output"}, "no findings to print: the review produced no output"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			writeFindingsJSONTestManifest(t, tt.report, true)
			called := false
			deps := Deps{TrailFindings: func(TrailReport) []Finding {
				called = true
				return nil
			}}

			stdout, stderr, err := runReviewCommand(deps, manifestFindingsFlag, findingsJSONTestHandle, "--json")
			if err == nil {
				t.Fatal("expected review --findings --json to fail")
			}
			if stdout != "" {
				t.Fatalf("stdout should be empty, got:\n%s", stdout)
			}
			if !strings.Contains(stderr, tt.want) {
				t.Fatalf("stderr should say %q, got:\n%s", tt.want, stderr)
			}
			if called {
				t.Fatal("the report was split although the run left none")
			}
		})
	}
}

func TestReviewJSONFlagNeedsShowConfigOrAFindingsHandle(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"alone", []string{"--json"}, "--json requires --show-config or --findings <handle>"},
		{"findings without a handle", []string{manifestFindingsFlag, "--json"}, "--findings --json needs a handle"},
		{"findings with an empty handle", []string{manifestFindingsFlag, "", "--json"}, "--findings --json needs a handle"},
		{"findings with a blank handle", []string{manifestFindingsFlag, "  ", "--json"}, "--findings --json needs a handle"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := runReviewCommand(Deps{}, tt.args...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}
