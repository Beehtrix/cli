package redact

import "strings"

// Passwords on command lines.
//
// A password typed on a command line is usually chosen by a person (hunter2,
// Summer2026!), far below entropyThreshold, so only its place gives it away:
// the value of a password flag, or the password of a .netrc entry. One table
// of flags serves two readings, which differ in how sure they are that the
// text is a command:
//
//   - Any text (detectCommandLinePasswords, part of the connection-string
//     layer) is read with care, because most text is not a command. A client's
//     short flags count only where its name starts a command, and the word
//     after a blank-separated long flag must not read as prose.
//   - A field an agent keeps the commands it ran in (commandFieldRegions and
//     argvRegions: "command" and "cmd" strings, their argument lists, and the
//     same keys inside a JSON-encoded tool call) is a command, so the word
//     after a password flag is hidden whenever it could be the value.
//
// The reader is a shell lexer, not a parser: words, quotes, escapes and the
// operators that end a simple command. A quoted word is read again as a
// command of its own, up to cmdMaxDepth deep, which covers `sh -c '...'`, ssh
// remote commands and command strings in code. Each level reads its text
// once, so the cost stays linear in the input.

const cmdMaxDepth = 3

// cmdPodman is podman, whose exec --latest names no container.
const cmdPodman = "podman"

// cmdPrefilterNames are the client names one of which a text must hold, as
// a word or the start of one (mysqldump, mongosh), before the lexer reads it.
// A long password flag is enough on its own.
var cmdPrefilterNames = []string{"mysql", "mariadb", "docker", cmdPodman, "nerdctl", "buildah", "skopeo",
	"oras", "helm", "az", "mongo", "sshpass", "redis-cli", "openssl", "psql", "pg_", "wget"}

// cmdWord is one shell word: its raw bytes in the text and its text with
// quotes and escapes removed.
type cmdWord struct {
	start, end  int
	text        string
	quoted      bool // some part was quoted
	leadQuoted  bool // the word starts with a quote
	wholeQuoted bool // the word is one quoted string
	dq          bool // read inside a double-quoted command string
	// sqFrom is where the single-quoted text that ends the word starts in
	// text, or -1. A value inside it is literal: '$X' is no reference.
	sqFrom int
}

// literalFrom reports whether the text from at on is single-quoted.
func (w cmdWord) literalFrom(at int) bool { return w.sqFrom >= 0 && w.sqFrom <= at }

// cmdHit is a password value: from byte offset at of word's text to its end.
type cmdHit struct{ word, at int }

// detectCommandLinePasswords returns the password regions in any text: flag
// values read with care, and .netrc entries.
func detectCommandLinePasswords(s string) []taggedRegion {
	return passwordRegions(s, true)
}

// commandRegions reads s as a command line an agent ran.
func commandRegions(s string) []taggedRegion {
	return passwordRegions(s, false)
}

func passwordRegions(s string, careful bool) []taggedRegion {
	regions := detectNetrcPasswords(s, careful)
	if hasCommandLineFlagHint(s) {
		lx := cmdLexer{s: s, careful: careful}
		lx.lex(0, len(s), 0, false)
		regions = append(regions, lx.regions...)
		regions = append(regions, codeListRegions(s)...)
	}
	return regions
}

func hasCommandLineFlagHint(s string) bool {
	if !strings.HasPrefix(s, "-") && !strings.Contains(s, " -") && !strings.Contains(s, `"-`) && !strings.Contains(s, `'-`) &&
		!strings.Contains(s, "\t-") && !strings.Contains(s, "\n-") {
		return false
	}
	if strings.Contains(s, "--pass") {
		return true
	}
	for _, name := range cmdPrefilterNames {
		if containsWordStart(s, name) {
			return true
		}
	}
	return false
}

// containsWordStart reports whether name starts a word of s: no letter,
// digit, dash or underscore comes just before it.
func containsWordStart(s, name string) bool {
	for i := 0; ; {
		j := strings.Index(s[i:], name)
		if j < 0 {
			return false
		}
		j += i
		if j == 0 || !isAlnum(s[j-1]) && s[j-1] != '-' && s[j-1] != '_' {
			return true
		}
		i = j + 1
	}
}

// cmdLexer splits text into simple commands and checks each one's words.
type cmdLexer struct {
	s       string
	careful bool // the text may not be a command at all
	regions []taggedRegion
	closes  map[int]int // each ( and { in s to its closing bracket
}

// lex reads s[from:to] as a command line. dqContent marks the inside of a
// double-quoted string, where \" quotes a word of the inner command.
func (lx *cmdLexer) lex(from, to, depth int, dqContent bool) {
	var words []cmdWord
	skipTarget := false
	for p := from; p < to; {
		next, sep := lx.separator(p, from, to)
		switch sep {
		case sepEnd:
			lx.check(words)
			words = words[:0]
		case sepRedirect:
			skipTarget = true
		case sepBlank:
		case sepNone:
			var w cmdWord
			w, next = lx.word(p, to, depth, dqContent)
			if !skipTarget && !isRedirectFD(lx.s, w, next, to) {
				words = append(words, w)
			}
			skipTarget = false
		}
		p = next
	}
	lx.check(words)
}

type cmdSeparator int

const (
	sepNone     cmdSeparator = iota // a word starts here
	sepBlank                        // blanks, a line continuation or a comment
	sepEnd                          // an operator that ends a simple command
	sepRedirect                     // a redirection, whose target is no argument
)

// separator reads what sits between words at p.
func (lx *cmdLexer) separator(p, from, to int) (int, cmdSeparator) {
	s := lx.s
	c := s[p]
	switch {
	case c == ' ' || c == '\t' || c == '\r':
		return p + 1, sepBlank
	case (c == '<' || c == '>') && p+1 < to && s[p+1] == '(' && lx.closed(p+1, to):
		return p, sepNone // a process substitution
	case c == '&' && p+1 < to && s[p+1] == '>':
		return skipBytes(s, p+2, to, ">|"), sepRedirect
	case c == '\n' || strings.IndexByte(";&|()", c) >= 0:
		return p + 1, sepEnd
	case c == '\\' && p+1 < to && s[p+1] == '\n':
		return p + 2, sepBlank
	case c == '#' && (p == from || isCmdBlank(s[p-1])):
		if nl := strings.IndexByte(s[p:to], '\n'); nl >= 0 {
			return p + nl, sepBlank
		}
		return to, sepBlank
	case c == '<' || c == '>':
		return skipBytes(s, p, to, "<>&|-"), sepRedirect
	}
	return p, sepNone
}

func skipBytes(s string, p, to int, set string) int {
	for p < to && strings.IndexByte(set, s[p]) >= 0 {
		p++
	}
	return p
}

// word reads the word at p, and reads again as commands the quoted pieces
// and substitutions inside it.
func (lx *cmdLexer) word(p, to, depth int, dqContent bool) (cmdWord, int) {
	if c := lx.s[p]; c == '<' || c == '>' {
		// A process substitution is one word, read again as a command.
		end, _ := lx.closingBracket(p+1, to)
		if depth < cmdMaxDepth {
			lx.lex(p+2, end-1, depth+1, false)
		}
		return cmdWord{start: p, end: end, text: lx.s[p:end], sqFrom: -1}, end
	}
	w, next, inner := lx.readWord(p, to, dqContent, nil)
	if depth < cmdMaxDepth {
		for _, q := range inner {
			lx.lex(q.start, q.end, depth+1, q.dq)
		}
	}
	return w, next
}

// isRedirectFD reports whether w is the file descriptor of a redirection
// that follows it: the 2 of 2>/dev/null.
func isRedirectFD(s string, w cmdWord, next, to int) bool {
	return next < to && (s[next] == '<' || s[next] == '>') && !w.quoted && isDigits(w.text)
}

func isCmdBlank(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

func isCmdWordEnd(c byte) bool {
	return isCmdBlank(c) || strings.IndexByte(";&|()<>", c) >= 0
}

// cmdQuoted is the inside of one quoted piece, to be read again as a command.
type cmdQuoted struct {
	start, end int
	dq         bool
}

// readWord reads the word at p. pos, when non-nil, receives the raw offset of
// each text byte, so a hit can be mapped back to the raw text.
func (lx *cmdLexer) readWord(p, to int, dqContent bool, pos *[]int) (cmdWord, int, []cmdQuoted) {
	s := lx.s
	if pos == nil {
		// Most words hold no quote, escape or substitution: their text is
		// their bytes, with nothing to copy.
		end := p
		for end < to && !isCmdWordEnd(s[end]) && strings.IndexByte("\\'\"`$", s[end]) < 0 {
			end++
		}
		if end == to || isCmdWordEnd(s[end]) {
			return cmdWord{start: p, end: end, text: s[p:end], sqFrom: -1, dq: dqContent}, end, nil
		}
	}
	b := wordBuilder{lx: lx, pos: pos, w: cmdWord{start: p, sqFrom: -1, dq: dqContent}}
	pieces, lastQuoted := 0, false
	for p < to && !isCmdWordEnd(s[p]) {
		b.endPiece()
		pieces++
		lastQuoted = s[p] == '\'' || s[p] == '"' || s[p] == '`'
		if pieces == 1 {
			b.w.leadQuoted = lastQuoted
		}
		p = b.piece(p, to, dqContent)
	}
	b.endPiece()
	b.w.end = p
	b.w.text = b.text.String()
	b.w.wholeQuoted = pieces == 1 && lastQuoted
	return b.w, p, b.inner
}

// wordBuilder collects one word's text, piece by piece.
type wordBuilder struct {
	lx    *cmdLexer
	w     cmdWord
	text  strings.Builder
	pos   *[]int
	inner []cmdQuoted
	// pieceStart is where the current piece's text starts, and literal
	// marks a single-quoted piece.
	pieceStart int
	literal    bool
	// expands is set when the piece holds a $ or a backquote the shell
	// expands.
	expands bool
}

func (b *wordBuilder) emit(at int) {
	b.text.WriteByte(b.lx.s[at])
	if b.pos != nil {
		*b.pos = append(*b.pos, at)
	}
}

func (b *wordBuilder) emitRange(from, to int) {
	for i := from; i < to; i++ {
		b.emit(i)
	}
}

// endPiece ends a piece. Text a shell expands after literal text makes the
// value no literal any more.
func (b *wordBuilder) endPiece() {
	if !b.literal && b.expands && b.text.Len() > b.pieceStart {
		b.w.sqFrom = -1
	}
	b.pieceStart, b.literal, b.expands = b.text.Len(), false, false
}

// markLiteral marks the piece about to be emitted as literal text without
// making the word a quoted one: an escaped $ or the inside of a double-quoted
// string that expands nothing.
func (b *wordBuilder) markLiteral(at int) {
	if b.w.sqFrom < 0 {
		b.w.sqFrom = at
	}
	b.literal = true
}

func (b *wordBuilder) startLiteral() {
	if b.w.sqFrom < 0 {
		b.w.sqFrom = b.text.Len()
	}
	b.literal, b.w.quoted = true, true
}

// piece reads one piece of a word at p and returns where it ends. A quote or
// substitution that never closes is plain text.
func (b *wordBuilder) piece(p, to int, dqContent bool) int {
	s := b.lx.s
	c := s[p]
	switch {
	case c == '\\' && dqContent && p+1 < to && s[p+1] == '"':
		// An escaped quote inside a double-quoted command string quotes a
		// word of that command.
		if end := strings.Index(s[p+2:to], `\"`); end >= 0 {
			b.emitRange(p+2, p+2+end)
			b.w.quoted = true
			return p + 2 + end + 2
		}
		b.emit(p + 1)
		return p + 2
	case c == '\\' && p+1 < to:
		if !dqContent && (s[p+1] == '$' || s[p+1] == '`') {
			b.markLiteral(b.text.Len())
		}
		b.emit(p + 1)
		return p + 2
	case c == '\'':
		if end := b.lx.closingQuote(p+1, to, '\'', false); end >= 0 {
			b.startLiteral()
			b.emitRange(p+1, end)
			b.inner = append(b.inner, cmdQuoted{start: p + 1, end: end})
			return end + 1
		}
	case c == '$' && p+1 < to && s[p+1] == '\'':
		// $'...' is literal too; its escapes are kept as written.
		if end := b.lx.closingQuote(p+2, to, '\'', true); end >= 0 {
			b.startLiteral()
			b.emitRange(p+2, end)
			return end + 1
		}
	case c == '"':
		if end := b.lx.closingQuote(p+1, to, '"', true); end >= 0 {
			b.doubleQuoted(p+1, end, !dqContent)
			return end + 1
		}
	case c == '`':
		// A backquoted substitution is part of the word, and is read again
		// as a command.
		if end := b.lx.closingQuote(p+1, to, '`', true); end >= 0 {
			b.expands = true
			b.emitRange(p, end+1)
			b.inner = append(b.inner, cmdQuoted{start: p + 1, end: end})
			return end + 1
		}
	case c == '$' && p+1 < to && (s[p+1] == '(' || s[p+1] == '{') && b.lx.closed(p+1, to):
		// A substitution or expansion is part of the word, and a
		// substitution is read again as a command.
		end, _ := b.lx.closingBracket(p+1, to)
		b.expands = true
		b.emitRange(p, end)
		if s[p+1] == '(' {
			b.inner = append(b.inner, cmdQuoted{start: p + 2, end: end - 1})
		}
		return end
	}
	b.expands = c == '$'
	b.emit(p)
	return p + 1
}

// doubleQuoted emits s[from:end], the inside of a double-quoted string,
// without the backslashes of its escapes. With literalOK, a string that
// expands nothing is literal text.
func (b *wordBuilder) doubleQuoted(from, end int, literalOK bool) {
	s := b.lx.s
	start := b.text.Len()
	for i := from; i < end; i++ {
		if s[i] == '\\' && i+1 < end && strings.IndexByte("\"\\$`", s[i+1]) >= 0 {
			i++
		} else if s[i] == '$' || s[i] == '`' {
			b.expands = true
		}
		b.emit(i)
	}
	if literalOK && !b.expands {
		b.markLiteral(start)
	}
	b.inner = append(b.inner, cmdQuoted{start: from, end: end, dq: true})
	b.w.quoted = true
}

// closingBracket returns the end of the bracketed text that opens at from,
// just past its closing bracket, when it closes before to.
func (lx *cmdLexer) closingBracket(from, to int) (int, bool) {
	if lx.closes == nil {
		lx.closes = matchBrackets(lx.s)
	}
	if c, ok := lx.closes[from]; ok && c < to {
		return c + 1, true
	}
	return 0, false
}

func (lx *cmdLexer) closed(from, to int) bool {
	_, ok := lx.closingBracket(from, to)
	return ok
}

// matchBrackets pairs every ( and { in s with the bracket that closes it,
// skipping escaped bytes. Pairing them once keeps an unclosed bracket from
// costing a scan to the end of the text each time it is met.
func matchBrackets(s string) map[int]int {
	closes := map[int]int{}
	var parens, braces []int
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '(':
			parens = append(parens, i)
		case '{':
			braces = append(braces, i)
		case ')':
			if n := len(parens); n > 0 {
				closes[parens[n-1]], parens = i, parens[:n-1]
			}
		case '}':
			if n := len(braces); n > 0 {
				closes[braces[n-1]], braces = i, braces[:n-1]
			}
		}
	}
	return closes
}

// closingQuote finds the quote q that closes one opened before from, or -1;
// escapes says a backslash escapes the byte after it. A careful reading keeps
// a quote on its line, so an apostrophe in prose does not pair with one many
// lines later; a command's quotes may span lines. A failed search means no
// such quote is left, so it never repeats.
func (lx *cmdLexer) closingQuote(from, to int, q byte, escapes bool) int {
	for i := from; i < to; i++ {
		switch c := lx.s[i]; {
		case c == q:
			return i
		case c == '\\' && escapes:
			i++
		case c == '\n' && lx.careful:
			return -1
		}
	}
	return -1
}

// check finds the password values in one simple command's words.
func (lx *cmdLexer) check(words []cmdWord) {
	if len(words) == 0 {
		return
	}
	for _, h := range commandHits(words, lx.careful) {
		lx.addHit(words[h.word], h.at)
	}
}

func (lx *cmdLexer) addHit(w cmdWord, at int) {
	var pos []int
	lx.readWord(w.start, w.end, w.dq, &pos)
	if at >= len(pos) {
		return
	}
	// From the value's first byte to its last, so the quotes around it
	// stay: -p'REDACTED'.
	start, end := pos[at], pos[len(pos)-1]+1
	if start < end {
		lx.regions = append(lx.regions, taggedRegion{region: region{start, end}, layer: layerConnectionString})
	}
}
