package redact

import (
	"net/netip"
	"regexp"
	"strings"
)

// IP address detection for the "ip" PII category.
//
// A bare dotted quad is ambiguous in coding transcripts: version numbers
// (1.17.2.3), section numbers and user agents (Chrome/154.0.0.0) have the same
// shape. Measured on real agent transcripts, matching every valid address
// redacted about one version number for every three real addresses. So a match
// is redacted only when it is shaped like an address in use, the approach
// Presidio and Cloud DLP take with context words:
//
//   - a private, link-local, or shared (CGNAT) address — internal network
//     layout, and almost never a version number; or
//   - network syntax around it: scheme (://), userinfo (@), port (:443),
//     CIDR prefix (/24), or a connection arrow (->); or
//   - a network word nearby (ip, host, server, ssh, tcp, dns, ...).
//
// A public address printed bare in a list with no such context is not
// redacted; custom_patterns covers teams that need every address.
var (
	ipv4CandidateRegex = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)
	// ipv6CandidateRegex needs at least two colons, so a single key:value
	// colon never becomes a candidate. The optional dotted tail admits
	// IPv4-embedded forms (::ffff:10.0.0.1). netip.ParseAddr does the actual
	// validation.
	ipv6CandidateRegex = regexp.MustCompile(`[0-9A-Fa-f]{0,4}(?::[0-9A-Fa-f]{0,4}){2,7}(?:\.\d{1,3}){0,3}`)

	ipContextWordRegex = regexp.MustCompile(`(?i)\b(?:ips?|ipv[46]|host(?:name|s)?|addr(?:ess(?:es)?)?|servers?|tcp|udp|ssh|inet6?|dns|nat|cidr|gateway|subnet|proxy|connect(?:ion|ed|ing|x)?|dial|ping|curl|nc|peer|remote|endpoint|resolve[sd]?|listen(?:ing)?|bind)\b`)
)

// ipContextBefore and ipContextAfter bound the window searched for a
// network word.
const (
	ipContextBefore = 60
	ipContextAfter  = 30
)

// cgnatPrefix is the shared address space (RFC 6598) carriers and overlay
// networks such as Tailscale use; netip does not classify it as private.
var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

// isRedactableIP reports whether the candidate s[start:end] is an IP address
// that should be redacted. It validates the address, rejects candidates that
// are part of a longer token, skips addresses that identify nothing
// (loopback, unspecified, 0.0.0.0/8, broadcast), and requires the context
// described above.
func isRedactableIP(s string, start, end int) bool {
	addr, err := netip.ParseAddr(s[start:end])
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	if !ipStandsAlone(s, start, end, addr) {
		return false
	}
	if addr.IsLoopback() || addr.IsUnspecified() {
		return false
	}
	if addr.Is4() {
		b := addr.As4()
		if b[0] == 0 || b == [4]byte{255, 255, 255, 255} {
			return false
		}
	}
	return ipHasContext(s, start, end, addr)
}

// ipStandsAlone rejects a candidate that continues a longer token on either
// side: an identifier or version (go1.2.3.4, v1.2.3.4, 1.2.3.4-rc1), a longer
// dotted number (an OID such as 1.3.6.1.4.1), a user agent (Chrome/154.0.0.0),
// or a code path (u64::from, a[1::2]).
func ipStandsAlone(s string, start, end int, addr netip.Addr) bool {
	if start > 0 {
		prev := s[start-1]
		if isIPWordByte(prev) || prev == '.' || prev == '\\' || (prev == ':' && addr.Is6()) {
			return false
		}
		if prev == '/' && start > 1 && isIPWordByte(s[start-2]) {
			return false
		}
	}
	if end < len(s) {
		next := s[end]
		if isIPWordByte(next) {
			return false
		}
		if (next == '.' || next == '-') && end+1 < len(s) && isIPWordByte(s[end+1]) {
			return false
		}
	}
	if addr.Is4() {
		return true
	}
	// IPv6: a short compressed form (fe80::1) is also a common code shape
	// (a[1::2], x::y), so a two-group address needs a clean left edge.
	candidate := s[start:end]
	if !strings.ContainsAny(candidate, "0123456789") {
		return false
	}
	if ipv6Groups(candidate) >= 3 {
		return true
	}
	if start == 0 {
		return true
	}
	switch s[start-1] {
	case ' ', '\t', '\n', '\r', '"', '\'', '`', '=', '(', ',', '@':
		return true
	case '[':
		return start >= 3 && s[start-3:start-1] == "//"
	}
	return false
}

// ipv6Groups counts the non-empty colon-separated groups of an IPv6 candidate.
func ipv6Groups(candidate string) int {
	n := 0
	for _, g := range strings.Split(candidate, ":") {
		if g != "" {
			n++
		}
	}
	return n
}

// ipHasContext reports whether the address is shaped like one in use; the
// rule is described at the top of this file.
func ipHasContext(s string, start, end int, addr netip.Addr) bool {
	if addr.IsPrivate() || addr.IsLinkLocalUnicast() || cgnatPrefix.Contains(addr) {
		return true
	}
	before, after := s[:start], s[end:]
	if strings.HasSuffix(before, "://") || strings.HasSuffix(before, "//[") ||
		strings.HasSuffix(before, "@") || strings.HasSuffix(before, "->") ||
		strings.HasPrefix(after, "->") || strings.HasPrefix(after, "]:") {
		return true
	}
	if len(after) >= 2 && (after[0] == ':' || after[0] == '/') && isDigit(after[1]) {
		return true
	}
	lo := max(start-ipContextBefore, 0)
	hi := min(end+ipContextAfter, len(s))
	return ipContextWordRegex.MatchString(s[lo:start]) || ipContextWordRegex.MatchString(s[end:hi])
}

// mayContainIPv4 reports whether s has a digit-dot-digit sequence, which
// every dotted quad contains.
func mayContainIPv4(s string) bool {
	for i := 1; i+1 < len(s); i++ {
		if s[i] == '.' && isDigit(s[i-1]) && isDigit(s[i+1]) {
			return true
		}
	}
	return false
}

// mayContainIPv6 reports whether s has "::" or a hex group between two
// colons (":ab:"), one of which every IPv6 address with three or more groups
// or a compressed form contains.
func mayContainIPv6(s string) bool {
	for i := strings.IndexByte(s, ':'); i >= 0 && i+1 < len(s); {
		j := i + 1
		for j < len(s) && j-i <= 5 && isHexByte(s[j]) {
			j++
		}
		if j < len(s) && s[j] == ':' {
			return true
		}
		next := strings.IndexByte(s[j:], ':')
		if next < 0 {
			return false
		}
		i = j + next
	}
	return false
}

func isHexByte(c byte) bool {
	return isDigit(c) || (c|0x20 >= 'a' && c|0x20 <= 'f')
}

func isIPWordByte(c byte) bool {
	return isDigit(c) || c == '_' || (c|0x20 >= 'a' && c|0x20 <= 'z')
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
