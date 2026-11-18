package redact

import "strings"

// Argument lists in code: subprocess.run(["mysql", "-pSecret"]),
// exec.Command("mysql", "-pSecret", db), new String[]{"wget", "--password",
// "Secret"}. A list starts at a string literal followed by a comma and runs
// while its elements are separated by commas; nested brackets are flattened.
// An element that is not one literal (a variable, a concatenation, an
// f-string) keeps its place but holds no value. The reader moves forward only,
// and a literal never spans a line, so the cost stays linear.

// codeHole stands for an element that is not one literal.
const codeHole = "\x00"

// codeScanner reads the code in s. A literal whose closing quote is not on
// its line is remembered: every later quote of its kind up to where that scan
// stopped fails too, since a scan from it is a suffix of the failed one in the
// same escape parity. So each line is scanned once per kind of quote.
type codeScanner struct {
	s    string
	fail [2]struct{ from, to int }
}

func codeListRegions(s string) []taggedRegion {
	c := &codeScanner{s: s}
	var regions []taggedRegion
	for p := 0; p < len(s); {
		q := strings.IndexAny(s[p:], `"'`)
		if q < 0 {
			break
		}
		q += p
		end, ok := c.literalEnd(q)
		if !ok {
			p = q + 1
			continue
		}
		if after := skipCodeBlanks(s, end+1); after >= len(s) || s[after] != ',' {
			p = end + 1
			continue
		}
		words, next := c.readList(q)
		regions = append(regions, codeListHits(words)...)
		p = max(next, end+1)
	}
	return regions
}

// readList reads the list whose first element starts at p and returns
// its elements and where it ends.
func (c *codeScanner) readList(p int) ([]cmdWord, int) {
	s := c.s
	var words []cmdWord
	depth := 0
	for p < len(s) {
		p = skipCodeBlanks(s, p)
		if p >= len(s) {
			break
		}
		switch s[p] {
		case ',':
			p++
			continue
		case '[', '{', '(':
			depth++
			p++
			continue
		case ']', '}', ')':
			p++
			if depth == 0 {
				return words, p
			}
			depth--
			continue
		}
		if w, next, ok := c.literal(p); ok {
			words = append(words, w)
			p = next
			continue
		}
		start := p
		p = c.skipElement(p)
		words = append(words, cmdWord{start: start, end: p, text: codeHole, sqFrom: -1})
		// An element ends at a comma or a closing bracket; anything else
		// ends the list.
		if after := skipCodeBlanks(s, p); after >= len(s) || strings.IndexByte(",)]}", s[after]) < 0 {
			return words, p
		}
	}
	return words, p
}

// literal reads the literal at p when it is a whole element: followed by
// a comma or a closing bracket.
func (c *codeScanner) literal(p int) (cmdWord, int, bool) {
	s := c.s
	if s[p] != '"' && s[p] != '\'' {
		return cmdWord{}, 0, false
	}
	end, ok := c.literalEnd(p)
	if !ok {
		return cmdWord{}, 0, false
	}
	after := skipCodeBlanks(s, end+1)
	if after >= len(s) || strings.IndexByte(",)]}", s[after]) < 0 {
		return cmdWord{}, 0, false
	}
	return cmdWord{start: p + 1, end: end, text: s[p+1 : end], sqFrom: -1}, after, true
}

// literalEnd returns the closing quote of the literal opened at q, on the
// same line.
func (c *codeScanner) literalEnd(q int) (int, bool) {
	s, k := c.s, 0
	if s[q] == '\'' {
		k = 1
	}
	if f := c.fail[k]; q >= f.from && q < f.to {
		return 0, false
	}
	i := q + 1
	for ; i < len(s) && s[i] != '\n'; i++ {
		switch s[i] {
		case s[q]:
			return i, true
		case '\\':
			i++
		}
	}
	c.fail[k].from, c.fail[k].to = q, i
	return 0, false
}

// skipCodeBlanks skips blanks, line breaks and line comments.
func skipCodeBlanks(s string, p int) int {
	for p < len(s) {
		switch c := s[p]; {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			p++
		case c == '#' || c == '/' && p+1 < len(s) && s[p+1] == '/':
			for p < len(s) && s[p] != '\n' {
				p++
			}
		default:
			return p
		}
	}
	return p
}

// skipElement skips an element that is not one literal, to the comma or
// closing bracket after it, or to the end of its line.
func (c *codeScanner) skipElement(p int) int {
	s := c.s
	depth := 0
	for p < len(s) {
		switch ch := s[p]; {
		case ch == '"' || ch == '\'':
			if end, ok := c.literalEnd(p); ok {
				p = end
			}
		case ch == '(' || ch == '[' || ch == '{':
			depth++
		case ch == ')' || ch == ']' || ch == '}':
			if depth == 0 {
				return p
			}
			depth--
		case ch == ',' && depth == 0, ch == '\n', ch == ';':
			return p
		}
		p++
	}
	return p
}

// codeListHits reads a list as an argument list. One that starts with a
// program name is a command; any other (flag names, words to print) is read
// with care.
func codeListHits(words []cmdWord) []taggedRegion {
	if len(words) < 2 {
		return nil
	}
	first := words[0].text
	careful := strings.HasPrefix(first, "-") || !isToolName(first[strings.LastIndexAny(first, `/\`)+1:])
	var regions []taggedRegion
	for _, h := range commandHits(words, careful) {
		if w := words[h.word]; w.text != codeHole {
			regions = append(regions, taggedRegion{region: region{w.start + h.at, w.end}, layer: layerConnectionString})
		}
	}
	return regions
}
