package review

import (
	"encoding/json"
	"strings"

	reviewtypes "github.com/entireio/cli/cmd/entire/cli/review/types"
)

// trailFindingsJSONInstructions is the trail-mode output contract. Reviewers
// and the judge end with this JSON line so posting reads their findings
// structurally instead of interpreting prose. The example line must parse
// with splitTrailJSON; a test enforces that.
const trailFindingsJSONInstructions = `Final line (required, read by a program):
End your output with exactly one line holding only this JSON object, with no code fence and nothing after it:
{"summary":"one-sentence verdict","comments":[{"severity":"high","body":"bug; impact; fix","location":{"granularity":"line","file_path":"path/to/file.go","start_line":42}}]}
- Add one comment per actionable finding reported above, with the same severity: high, medium, or low.
- location.granularity is line (with start_line), range (with start_line and end_line), file, or whole_change.
- If nothing is actionable, use an empty array: "comments":[].
- This line is required even where other instructions say to output nothing else.`

// maxTrailJSONLines bounds the backward scan for a pretty-printed JSON block.
const maxTrailJSONLines = 200

// TrailComment is one finding in the trail JSON contract.
type TrailComment struct {
	Severity   string        `json:"severity"`
	Confidence *float64      `json:"confidence,omitempty"`
	Body       string        `json:"body"`
	Location   TrailLocation `json:"location"`
}

// TrailLocation anchors a TrailComment to the change.
type TrailLocation struct {
	Granularity  string `json:"granularity"`
	FilePath     string `json:"file_path,omitempty"`
	StartLine    int    `json:"start_line,omitempty"`
	EndLine      int    `json:"end_line,omitempty"`
	SelectedText string `json:"selected_text,omitempty"`
}

// TrailReport is what a trail-mode run posts: structured comments when the
// review honored the JSON contract, otherwise its prose with any contract
// line removed. Exactly one of the two is set.
type TrailReport struct {
	Comments []TrailComment
	Prose    string
}

// trailReportFor decides what a trail-mode run posts. It returns post=false
// only when the JSON contract reports no findings and nothing in the prose
// contradicts that; every malformed or contradictory case posts, so a
// misbehaving model costs an extra finding rather than a lost one. When a
// judge ran, its verdict decides: it is asked to drop unsupported claims.
func trailReportFor(summary reviewtypes.RunSummary, aggregateOutput string) (TrailReport, bool) {
	if verdict := strings.TrimSpace(aggregateOutput); verdict != "" {
		prose, comments, ok := splitTrailJSON(verdict)
		if prose == "" {
			prose = verdict
		}
		if !ok || (len(comments) == 0 && proseReportsFindings(prose)) {
			return TrailReport{Prose: prose}, true
		}
		return TrailReport{Comments: comments}, len(comments) > 0
	}
	var merged []TrailComment
	for _, run := range usableAgentRuns(summary) {
		prose, comments, ok := splitTrailJSON(joinAssistantText(run.Buffer))
		if !ok || (len(comments) == 0 && proseReportsFindings(prose)) {
			return TrailReport{Prose: combinedReviewNarratives(summary)}, true
		}
		merged = append(merged, comments...)
	}
	return TrailReport{Comments: merged}, len(merged) > 0
}

// splitTrailJSON separates a trailing JSON object (one line, a fenced block,
// or a pretty-printed block) from the prose before it. Any trailing JSON
// object is removed from prose so it is never posted as finding text. ok
// reports a valid contract: a "comments" array. Entries without a body are
// dropped.
func splitTrailJSON(text string) (prose string, comments []TrailComment, ok bool) {
	text = strings.TrimSpace(text)
	lines := strings.Split(text, "\n")
	end := len(lines)
	fenced := end > 1 && strings.TrimSpace(lines[end-1]) == "```"
	if fenced {
		end--
	}
	for start := end - 1; start >= 0 && start >= end-maxTrailJSONLines; start-- {
		if !strings.HasPrefix(strings.TrimSpace(lines[start]), "{") {
			continue
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal([]byte(strings.Join(lines[start:end], "\n")), &payload); err != nil {
			continue
		}
		proseEnd := start
		if fenced && proseEnd > 0 && strings.HasPrefix(strings.TrimSpace(lines[proseEnd-1]), "```") {
			proseEnd--
		}
		prose = strings.TrimSpace(strings.Join(lines[:proseEnd], "\n"))
		comments, ok = decodeTrailComments(payload["comments"])
		return prose, comments, ok
	}
	return text, nil, false
}

func decodeTrailComments(raw json.RawMessage) ([]TrailComment, bool) {
	if !strings.HasPrefix(strings.TrimSpace(string(raw)), "[") {
		return nil, false
	}
	var decoded []TrailComment
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, false
	}
	comments := []TrailComment{}
	for _, comment := range decoded {
		if strings.TrimSpace(comment.Body) != "" {
			comments = append(comments, comment)
		}
	}
	return comments, true
}

// stripTrailJSON removes a valid trailing contract line for display.
func stripTrailJSON(text string) string {
	if prose, _, ok := splitTrailJSON(text); ok {
		return prose
	}
	return text
}

// proseReportsFindings reports whether prose contradicts an empty comments
// array: a list item, severity marker, or feedback label such as "Nits:" at
// any indentation, or a verdict, labeled or not, that itself says there is
// feedback. These are the shapes the output format asks for, so a clean
// review has none of them.
func proseReportsFindings(prose string) bool {
	for _, line := range strings.Split(prose, "\n") {
		trimmed := strings.TrimSpace(line)
		if isListItem(trimmed) {
			return true
		}
		lower := strings.ToLower(trimMarkdownEmphasis(trimmed))
		if after, found := strings.CutPrefix(lower, "verdict:"); found {
			lower = trimMarkdownEmphasis(after)
		}
		if startsWithSeverityMarker(lower) {
			return true
		}
		for _, prefix := range []string{"request changes", "request_changes", "changes requested", "approve with nits", "nits:", "issues:", "concerns:", "suggestions:"} {
			if strings.HasPrefix(lower, prefix) {
				return true
			}
		}
	}
	return false
}

func trimMarkdownEmphasis(s string) string {
	return strings.TrimLeft(s, "#>*_` ")
}

// startsWithSeverityMarker reports whether lower opens with a severity tag in
// the bracketed ("[high]") or delimited ("high —", "p2:") form.
func startsWithSeverityMarker(lower string) bool {
	for _, marker := range []string{"critical", "high", "medium", "low", "p0", "p1", "p2", "p3", "nit"} {
		if strings.HasPrefix(lower, "["+marker+"]") {
			return true
		}
		for _, suffix := range []string{" —", " -", ":"} {
			if strings.HasPrefix(lower, marker+suffix) {
				return true
			}
		}
	}
	return false
}

func isListItem(trimmed string) bool {
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
		return true
	}
	digits := len(trimmed) - len(strings.TrimLeft(trimmed, "0123456789"))
	rest := trimmed[digits:]
	return digits > 0 && (strings.HasPrefix(rest, ". ") || strings.HasPrefix(rest, ") "))
}
