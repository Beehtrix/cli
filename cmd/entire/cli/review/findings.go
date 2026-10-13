package review

import "strings"

// TopLevelBulletText returns the text of a top-level Markdown bullet or
// numbered item. Indented lines are continuations, not new findings.
func TopLevelBulletText(line string) (string, bool) {
	trimmed, ok := topLevelLine(line)
	if !ok || len(trimmed) < 3 {
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
	if !ok || !StartsWithReviewSeverityMarker(trimmed) {
		return "", false
	}
	return trimmed, true
}

// IsReviewFindingLine reports whether a line has the shape the reviewer
// prompts mandate for a finding: a top-level bullet or a severity marker.
func IsReviewFindingLine(line string) bool {
	if _, ok := TopLevelBulletText(line); ok {
		return true
	}
	_, ok := TopLevelMarkedFindingText(line)
	return ok
}

func topLevelLine(line string) (string, bool) {
	trimmedRight := strings.TrimRight(line, " \t")
	if len(trimmedRight) != len(strings.TrimLeft(trimmedRight, " \t")) {
		return "", false
	}
	return strings.TrimSpace(trimmedRight), true
}

// StartsWithReviewSeverityMarker reports whether s opens with a severity tag
// in either the bracketed ("[high]") or delimited ("high —", "P2:") form.
func StartsWithReviewSeverityMarker(s string) bool {
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
