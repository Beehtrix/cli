package review

import (
	"strings"
	"unicode"
	"unicode/utf8"

	reviewtypes "github.com/entireio/cli/cmd/entire/cli/review/types"
)

// reviewVerdictQualifiers mark an approve reason that still carries feedback
// or a condition, so it must be posted rather than treated as clean.
var reviewVerdictQualifiers = map[string]bool{
	"nit": true, "nits": true, "but": true, "however": true,
	"once": true, "after": true, "pending": true, "unless": true,
	"if": true, "provided": true, "assuming": true, "when": true,
	"until": true, "suggestion": true, "suggestions": true, "minor": true,
	"consider": true, "should": true,
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
// contain no other verdict, and to carry no finding-shaped line anywhere. A
// trailing approve therefore cannot erase an earlier request for changes or
// an earlier finding bullet.
func isCleanReviewVerdict(text string) bool {
	clean, isVerdict := false, false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if IsReviewFindingLine(raw) {
			return false
		}
		clean, isVerdict = parseReviewVerdictLine(line)
		if isVerdict && !clean {
			return false
		}
	}
	return isVerdict && clean
}

// parseReviewVerdictLine classifies one line. An approve is clean only when
// nothing follows it, or when a separator introduces a reason free of
// qualifiers. A word directly after "approve" ("approve if", "approve with
// suggestions") is a condition, so unknown phrasings are posted, not dropped.
func parseReviewVerdictLine(line string) (clean, isVerdict bool) {
	line = strings.ToLower(line)
	line = strings.TrimSpace(strings.TrimLeft(line, "#>"))
	line = strings.TrimLeft(line, "*_`")
	if strings.HasPrefix(line, "request changes") {
		return false, true
	}
	rest, ok := strings.CutPrefix(line, "approve")
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
	if first, _ := utf8.DecodeRuneInString(rest); unicode.IsLetter(first) && !strings.HasPrefix(rest, "with no") {
		return false, true
	}
	for _, word := range strings.FieldsFunc(rest, func(r rune) bool { return !unicode.IsLetter(r) }) {
		if reviewVerdictQualifiers[word] {
			return false, true
		}
	}
	return true, true
}
