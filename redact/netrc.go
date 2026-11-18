package redact

import "strings"

// detectNetrcPasswords returns the password of every .netrc entry in s, as a
// file holds it or as a command writes it (echo, printf with \n or \t, a
// here-document).
//
// An entry starts with "machine NAME" or "default" at the start of a line or
// of a quoted string, then alternates keywords (login, account, password) and
// their values, in any case. The entry ends at the first word in a keyword's
// place that is no keyword. In a command every entry counts. In other text an
// entry counts once it has a login or an account or its machine is named like
// a host, and only when it ends cleanly (at the end of a line, a quote or a
// new entry) with no type name for a password, so prose such as "machine
// learning password policy" or a struct of strings is no entry. One pass, so
// the cost is linear in s.
// The .netrc keywords the reader follows.
const (
	netrcMachine  = "machine"
	netrcDefault  = "default"
	netrcLogin    = "login"
	netrcAccount  = "account"
	netrcPassword = "password"
)

func detectNetrcPasswords(s string, careful bool) []taggedRegion {
	if !containsFoldASCII(s, netrcPassword) {
		return nil
	}
	n := netrcScanner{s: s, lineStart: true}
	e := netrcEntries{careful: careful}
	for {
		start, stop, lineStart, ok := n.next(e.inEntry && e.expect != "")
		if !ok {
			break
		}
		tok := s[start:stop]
		switch {
		case e.inEntry && e.expect != "" && tok == "#":
			n.skipLine() // a comment before the value
		case e.inEntry && e.expect != "":
			e.value(tok, start, stop)
		case strings.HasPrefix(tok, "#") && e.inEntry:
			n.skipLine()
		default:
			e.keyword(tok, lineStart, stop == start)
		}
	}
	e.end(true)
	return e.regions
}

// netrcEntries follows the entry being read.
type netrcEntries struct {
	careful          bool
	regions, pending []taggedRegion
	inEntry, named   bool
	sure             bool   // the entry is a .netrc entry, not prose
	expect           string // the keyword whose value comes next
}

func (e *netrcEntries) end(clean bool) {
	if e.sure && clean || !e.careful {
		e.regions = append(e.regions, e.pending...)
	}
	e.pending, e.inEntry, e.named, e.sure, e.expect = e.pending[:0], false, false, false, ""
}

func (e *netrcEntries) value(tok string, start, stop int) {
	if e.expect == netrcPassword && stop > start && isNetrcSecret(tok, e.careful) {
		e.pending = append(e.pending, taggedRegion{region: region{start, stop}, layer: layerConnectionString})
	}
	e.sure = e.sure || e.expect == netrcLogin || e.expect == netrcAccount || e.expect == netrcMachine && isHostLike(tok)
	e.named = e.named || e.expect == netrcMachine
	e.expect = ""
}

// keyword reads a word in a keyword's place.
func (e *netrcEntries) keyword(tok string, lineStart, empty bool) {
	switch kw := strings.ToLower(tok); {
	case kw == netrcMachine || kw == netrcDefault:
		if !e.inEntry && !lineStart {
			return
		}
		e.end(true)
		e.inEntry, e.named, e.sure = true, kw == netrcDefault, kw == netrcDefault
		if kw == netrcMachine {
			e.expect = netrcMachine
		}
	case !e.inEntry:
	case e.named && (kw == netrcLogin || kw == netrcAccount || kw == netrcPassword):
		e.expect = kw
	default:
		// A word in a keyword's place ends the entry; prose goes on along
		// the line, while a file's entry ends with it.
		e.end(lineStart || empty)
	}
}

// isNetrcSecret reports whether a .netrc password value can be a password: no
// placeholder or format verb, and, in text that may not be a file, no type
// name (password string).
func isNetrcSecret(v string, careful bool) bool {
	if isPlaceholderSecretValueQuoted(v, true) || isFormatPlaceholder(v) {
		return false
	}
	if careful {
		switch strings.ToLower(v) {
		case "string", "str", "int", "bool", "bytes", "[]byte", "text", "any", "char", "varchar", "secretstr":
			return false
		}
	}
	return true
}

// isHostLike reports whether a machine name is written like a host: a
// dotted name or address, an IPv6 address, or localhost.
func isHostLike(name string) bool {
	return strings.ContainsAny(name, ".:") || strings.EqualFold(name, "localhost")
}

// netrcScanner splits text into .netrc tokens. Blanks, line breaks and the
// \n, \r and \t escapes a printf format holds separate them, and a quote or
// shell operator ends one, as it ends a shell word.
const netrcMaxQuoted = 256

type netrcScanner struct {
	s         string
	p         int
	lineStart bool
}

// next returns the next token's bounds and whether it starts a line or a
// quoted string. A value may be quoted; elsewhere a quote only opens the
// string a command writes. A token that is only a stop byte is returned empty.
func (n *netrcScanner) next(value bool) (start, stop int, lineStart, ok bool) {
	s := n.s
	for n.p < len(s) {
		c := s[n.p]
		switch {
		case c == ' ' || c == '\t':
			n.p++
			continue
		case c == '\n' || c == '\r':
			n.lineStart = true
			n.p++
			continue
		case c == '\\' && n.p+1 < len(s) && strings.IndexByte("nrt", s[n.p+1]) >= 0:
			n.lineStart = n.lineStart || s[n.p+1] != 't'
			n.p += 2
			continue
		}
		break
	}
	if n.p >= len(s) {
		return 0, 0, false, false
	}
	lineStart = n.lineStart
	n.lineStart = false
	if c := s[n.p]; c == '"' || c == '\'' {
		n.p++
		lineStart = true
		// A quoted value runs to its closing quote within netrcMaxQuoted
		// bytes on the same line; the bound keeps the scan linear.
		if value {
			window := s[n.p:min(len(s), n.p+netrcMaxQuoted)]
			if nl := strings.IndexByte(window, '\n'); nl >= 0 {
				window = window[:nl]
			}
			// A value starts and ends with no blank: in `password " + pw + "`
			// the quotes belong to code.
			if end := netrcQuoteEnd(window, c); end > 0 && !isCmdBlank(window[0]) && !isCmdBlank(window[end-1]) &&
				(n.p+end+1 >= len(s) || isNetrcBreak(s, n.p+end+1)) {
				start, stop = n.p, n.p+end
				n.p = stop + 1
				return start, stop, lineStart, true
			}
		}
	}
	// An unquoted value ends at a blank, or at the quote that closes the
	// shell string an entry is written in. Shell operators are part of it:
	// an entry is file text, never a command.
	start = n.p
	for n.p < len(s) && !isNetrcBreak(s, n.p) && strings.IndexByte("\"'`", s[n.p]) < 0 {
		n.p++
	}
	stop = n.p
	if stop == start {
		n.p++ // a lone stop byte
	}
	return start, stop, lineStart, true
}

// netrcQuoteEnd returns the quote q that closes a quoted value in window,
// past backslash escapes, or -1.
func netrcQuoteEnd(window string, q byte) int {
	for i := 0; i < len(window); i++ {
		switch window[i] {
		case '\\':
			i++
		case q:
			return i
		}
	}
	return -1
}

func isNetrcBreak(s string, i int) bool {
	c := s[i]
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' ||
		c == '\\' && i+1 < len(s) && strings.IndexByte("nrt", s[i+1]) >= 0
}

// skipLine skips a comment to the end of its line.
func (n *netrcScanner) skipLine() {
	for n.p < len(n.s) && n.s[n.p] != '\n' && !strings.HasPrefix(n.s[n.p:], `\n`) {
		n.p++
	}
}
