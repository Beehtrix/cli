package review

import (
	"strings"
	"unicode"
	"unicode/utf8"

	reviewtypes "github.com/entireio/cli/cmd/entire/cli/review/types"
)

// reviewVerdictQualifiers mark an approve reason that still carries feedback
// or a condition, so it must be posted rather than treated as clean. The list
// is deliberately broad: a clean reason that happens to use one of these words
// is posted, which only restores the previous behavior.
var reviewVerdictQualifiers = map[string]bool{
	"nit": true, "nits": true, "but": true, "however": true,
	"once": true, "after": true, "pending": true, "unless": true,
	"if": true, "provided": true, "assuming": true, "when": true,
	"until": true, "suggestion": true, "suggestions": true, "minor": true,
	"consider": true, "should": true, "though": true, "although": true,
	"except": true,
}

// reviewNoIssueNouns complete a "no ..." reason that says nothing needs fixing,
// such as "no actionable findings" or "no blocking issues".
var reviewNoIssueNouns = map[string]bool{
	"finding": true, "findings": true, "issue": true, "issues": true,
	"defect": true, "defects": true, "problem": true, "problems": true,
	"concern": true, "concerns": true,
}

// isNoIssueReason reports whether an approve reason states that nothing needs
// fixing. Any other reason may be feedback, so it is posted.
func isNoIssueReason(words []string) bool {
	if len(words) > 0 && words[0] == "with" {
		words = words[1:]
	}
	if len(words) == 0 {
		return false
	}
	switch words[0] {
	case "nothing", "lgtm":
		return true
	case "looks", "all":
		return len(words) > 1 && words[1] == "good"
	case "no":
		for _, word := range words[1:min(len(words), 4)] {
			if reviewNoIssueNouns[word] {
				return true
			}
		}
	}
	return false
}

// allReviewNarrativesClean reports whether every usable reviewer approved
// without qualification, so one clean reviewer cannot hide another's findings.
func allReviewNarrativesClean(summary reviewtypes.RunSummary) bool {
	for _, run := range usableAgentRuns(summary) {
		if narrative := joinAssistantText(run.Buffer); narrative != "" && !isCleanReviewVerdict(narrative) {
			return false
		}
	}
	return true
}

// isCleanReviewVerdict requires the text to end in an unqualified approve, to
// contain no other verdict, and to carry no finding-shaped line or file:line
// pointer anywhere. A trailing approve therefore cannot erase an earlier
// request for changes, finding bullet, or prose finding that cites a location.
// Prose after the verdict line is posted rather than dropped, since it could
// be a finding stated without a bullet.
func isCleanReviewVerdict(text string) bool {
	clean, isVerdict := false, false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if isFindingShapedLine(line) || LocationPointerPattern.MatchString(line) {
			return false
		}
		clean, isVerdict = parseReviewVerdictLine(line)
		if isVerdict && !clean {
			return false
		}
	}
	return isVerdict && clean
}

// parseReviewVerdictLine classifies one line. An approve (or approved) is
// clean only when nothing follows it, or when its reason says nothing needs
// fixing ("no actionable findings", "looks good") and carries no qualifier.
// A word directly after "approve" other than "with no"/"with nothing"
// ("approve if", "approve with suggestions") is a condition, and any other
// reason may be feedback, so unknown phrasings are posted, not dropped. A labelled or bulleted verdict ("Verdict: approve", "- approve") is
// not recognized and is posted too.
func parseReviewVerdictLine(line string) (clean, isVerdict bool) {
	line = strings.ToLower(line)
	line = strings.TrimSpace(strings.TrimLeft(line, "#>"))
	line = strings.TrimLeft(line, "*_`")
	if strings.HasPrefix(line, "request changes") {
		return false, true
	}
	rest, ok := strings.CutPrefix(line, "approved")
	if !ok {
		rest, ok = strings.CutPrefix(line, "approve")
	}
	if !ok {
		return false, false
	}
	if rest == "" {
		return true, true
	}
	first, _ := utf8.DecodeRuneInString(rest)
	if !unicode.IsSpace(first) && !unicode.IsPunct(first) && !unicode.IsSymbol(first) {
		return false, false
	}
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return true, true
	}
	words := strings.FieldsFunc(rest, func(r rune) bool { return !unicode.IsLetter(r) })
	if len(words) == 0 {
		return true, true
	}
	if first, _ := utf8.DecodeRuneInString(rest); unicode.IsLetter(first) && words[0] != "with" {
		return false, true
	}
	for _, word := range words {
		if reviewVerdictQualifiers[word] {
			return false, true
		}
	}
	return isNoIssueReason(words), true
}
