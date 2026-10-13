package review

import (
	"regexp"
	"strings"
)

// LocationPointerPattern matches a file:line pointer such as "retry.go:42",
// the shape the reviewer prompts ask for in every finding.
var LocationPointerPattern = regexp.MustCompile("(?:^|[\\s(`])([A-Za-z0-9_./-]+\\.[A-Za-z0-9_+-]+):(\\d+)")

// TopLevelBulletText returns the text of a top-level Markdown bullet or
// numbered item. Indented lines are continuations, not new findings.
func TopLevelBulletText(line string) (string, bool) {
	trimmed, ok := topLevelLine(line)
	if !ok {
		return "", false
	}
	return bulletText(trimmed)
}

func bulletText(trimmed string) (string, bool) {
	if len(trimmed) < 3 {
		return "", false
	}
	if strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") || strings.HasPrefix(trimmed, "+ ") {
		return strings.TrimSpace(trimmed[2:]), true
	}
	for i, r := range trimmed {
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '.' && i > 0 && i+1 < len(trimmed) && trimmed[i+1] == ' ' {
			return strings.TrimSpace(trimmed[i+2:]), true
		}
		return "", false
	}
	return "", false
}

// TopLevelMarkedFindingText returns a top-level line that opens with a
// severity marker, such as "[high] file:line — ..." without a leading bullet.
func TopLevelMarkedFindingText(line string) (string, bool) {
	trimmed, ok := topLevelLine(line)
	if !ok || !startsWithReviewSeverityMarker(trimmed) {
		return "", false
	}
	return trimmed, true
}

// isFindingShapedLine reports whether a line at any indentation looks like a
// finding: a bullet, a numbered item, or a severity marker. Unlike the
// splitter, the clean-verdict check must not read an indented finding as a
// continuation, or a trailing approve would hide it.
func isFindingShapedLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if _, ok := bulletText(trimmed); ok {
		return true
	}
	return startsWithReviewSeverityMarker(trimmed)
}

func topLevelLine(line string) (string, bool) {
	trimmedRight := strings.TrimRight(line, " \t")
	if len(trimmedRight) != len(strings.TrimLeft(trimmedRight, " \t")) {
		return "", false
	}
	return strings.TrimSpace(trimmedRight), true
}

// startsWithReviewSeverityMarker reports whether s opens with a severity tag
// in either the bracketed ("[high]") or delimited ("high —", "P2:") form.
func startsWithReviewSeverityMarker(s string) bool {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "**")
	s = strings.TrimPrefix(s, "__")
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	for _, marker := range []string{"critical", "high", "medium", "low", "p0", "p1", "p2", "p3", "nit"} {
		for _, suffix := range []string{"]", " —", " -", ":"} {
			prefix := marker + suffix
			if suffix == "]" {
				prefix = "[" + prefix
			}
			if strings.HasPrefix(lower, prefix) {
				return true
			}
		}
	}
	return false
}
