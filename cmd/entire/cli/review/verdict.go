package review

import (
	"strings"
	"unicode"
	"unicode/utf8"

	reviewtypes "github.com/entireio/cli/cmd/entire/cli/review/types"
)

// reviewVerdictQualifiers mark an approve that still carries feedback or a
// condition, so it must be posted rather than treated as clean.
var reviewVerdictQualifiers = map[string]bool{
	"nit": true, "nits": true, "but": true, "however": true,
	"once": true, "after": true, "pending": true, "unless": true,
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

// isCleanReviewVerdict requires the text to end in an unqualified approve and
// to contain no other verdict. Prompts put the verdict first, so a trailing
// approve cannot override an earlier request for changes.
func isCleanReviewVerdict(text string) bool {
	clean, isVerdict := false, false
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		clean, isVerdict = parseReviewVerdictLine(line)
		if isVerdict && !clean {
			return false
		}
	}
	return isVerdict && clean
}

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
	if rest != "" {
		first, _ := utf8.DecodeRuneInString(rest)
		if !unicode.IsSpace(first) && !unicode.IsPunct(first) && !unicode.IsSymbol(first) {
			return false, false
		}
	}
	words := strings.FieldsFunc(rest, func(r rune) bool { return !unicode.IsLetter(r) })
	for _, word := range words {
		if reviewVerdictQualifiers[word] {
			return false, true
		}
	}
	return true, true
}
