package redact

import (
	"encoding/json"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// isCommandFieldKey reports whether a JSON key holds a command an agent ran:
// Claude Code, Gemini CLI and OpenCode keep it under "command", Codex under
// "cmd" (exec_command) and as an argument list under "command".
func isCommandFieldKey(key string) bool {
	if len(key) > 16 {
		return false
	}
	// Lowercased with "_" and "-" dropped, in place: this runs for every
	// string in a transcript.
	var buf [16]byte
	n := 0
	for i := range len(key) {
		c := key[i]
		switch {
		case c == '_' || c == '-':
			continue
		case c >= 'A' && c <= 'Z':
			c += 'a' - 'A'
		}
		buf[n] = c
		n++
	}
	switch string(buf[:n]) {
	case "command", "cmd", "commandline", "shellcommand":
		return true
	}
	return false
}

// commandFieldRegions returns the command-line regions a leaf's key adds: the
// leaf read as a command when its key names one, or the commands inside a
// JSON-encoded tool call (Codex's function_call arguments).
func commandFieldRegions(key, val string) []taggedRegion {
	if isCommandFieldKey(key) {
		return commandRegions(val)
	}
	return embeddedCommandRegions(val)
}

// commandArgvRegions returns argvRegions for an array under a command key
// whose strings are scanned, and nil for any other array.
func commandArgvRegions(key string, skipStrings bool, arr []any) [][]taggedRegion {
	if skipStrings || !isCommandFieldKey(key) {
		return nil
	}
	return argvRegions(arr)
}

func elemRegions(byElem [][]taggedRegion, i int) []taggedRegion {
	if byElem == nil {
		return nil
	}
	return byElem[i]
}

// argvRegions returns, for an argument list under a command key, the regions
// of each element that is a password: ["mysql","-phunter2"] or
// ["docker","login","-p","hunter2"]. nil when arr is no list of at least two
// strings. An element reaches the program as written, but "-p$DB_PASS" there
// is a placeholder someone meant a shell to fill, so values are not read as
// literal.
func argvRegions(arr []any) [][]taggedRegion {
	words, ok := argvWords(arr)
	if !ok {
		return nil
	}
	hits := append(commandHits(words, false), argvNetrcHits(words)...)
	if len(hits) == 0 {
		return nil
	}
	out := make([][]taggedRegion, len(arr))
	for _, h := range hits {
		w := words[h.word]
		out[h.word] = append(out[h.word], taggedRegion{region: region{h.at, len(w.text)}, layer: layerConnectionString})
	}
	return out
}

// argvNetrcHits reads .netrc keywords written one per element, as
// ["echo","machine","h","login","me","password","hunter2"] writes them.
func argvNetrcHits(words []cmdWord) []cmdHit {
	var hits []cmdHit
	named := false
	for i := 0; i+1 < len(words); i++ {
		switch strings.ToLower(words[i].text) {
		case netrcMachine:
			named = true
			i++
		case netrcDefault:
			named = true
		case netrcLogin, netrcAccount:
			if named {
				i++
			}
		case netrcPassword:
			if named && isNetrcSecret(words[i+1].text, false) {
				hits = append(hits, cmdHit{word: i + 1})
				i++
			}
		}
	}
	return hits
}

func argvWords(arr []any) ([]cmdWord, bool) {
	if len(arr) < 2 {
		return nil, false
	}
	words := make([]cmdWord, len(arr))
	for i, v := range arr {
		s, ok := v.(string)
		if !ok {
			return nil, false
		}
		words[i] = cmdWord{end: len(s), text: s, sqFrom: -1}
	}
	return words, true
}

// embeddedCommandRegions reads the command fields of a JSON object written
// into a string, as Codex writes a tool call's arguments
// ({"cmd":"mysql -p...","workdir":"/repo"}), and maps what it finds back to
// the encoded text.
func embeddedCommandRegions(s string) []taggedRegion {
	if len(s) < 2 || s[0] != '{' || !strings.Contains(s, `"cmd"`) && !strings.Contains(s, `"command"`) {
		return nil
	}
	dec := json.NewDecoder(strings.NewReader(s))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil
	}
	var regions []taggedRegion
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return regions
		}
		key, ok := tok.(string)
		if !ok {
			return regions
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return regions
		}
		if !isCommandFieldKey(key) {
			continue
		}
		end := int(dec.InputOffset())
		regions = append(regions, encodedCommandRegions(s, end-len(raw), end)...)
	}
	return regions
}

// encodedCommandRegions reads the JSON value s[start:end], a command string
// or an argument list, and returns its password regions in s.
func encodedCommandRegions(s string, start, end int) []taggedRegion {
	if s[start] == '"' {
		text, pos, ok := decodeJSONStringAt(s, start)
		if !ok {
			return nil
		}
		return mapRegions(commandRegions(text), pos)
	}
	if s[start] != '[' {
		return nil
	}
	var texts []string
	var positions [][]int
	for p := start + 1; p < end; p++ {
		if s[p] != '"' {
			continue
		}
		text, pos, ok := decodeJSONStringAt(s, p)
		if !ok {
			return nil
		}
		texts, positions = append(texts, text), append(positions, pos)
		p = pos[len(pos)-1]
	}
	arr := make([]any, len(texts))
	for i, t := range texts {
		arr[i] = t
	}
	var regions []taggedRegion
	for i, rs := range argvRegions(arr) {
		regions = append(regions, mapRegions(rs, positions[i])...)
	}
	for i, t := range texts {
		regions = append(regions, mapRegions(commandRegions(t), positions[i])...)
	}
	return regions
}

func mapRegions(rs []taggedRegion, pos []int) []taggedRegion {
	for i := range rs {
		rs[i].start, rs[i].end = pos[rs[i].start], pos[rs[i].end]
	}
	return rs
}

// decodeJSONStringAt decodes the JSON string whose opening quote is at q.
// pos[i] is where the decoded byte i starts in s, and pos[len(text)] is the
// closing quote.
func decodeJSONStringAt(s string, q int) (string, []int, bool) {
	var b strings.Builder
	var pos []int
	for p := q + 1; p < len(s); {
		c := s[p]
		switch {
		case c == '"':
			return b.String(), append(pos, p), true
		case c != '\\':
			b.WriteByte(c)
			pos = append(pos, p)
			p++
			continue
		case p+1 >= len(s):
			return "", nil, false
		}
		at := p
		var r rune
		switch e := s[p+1]; e {
		case '"', '\\', '/':
			r, p = rune(e), p+2
		case 'b', 'f', 'n', 'r', 't':
			r, p = rune("\b\f\n\r\t"[strings.IndexByte("bfnrt", e)]), p+2
		case 'u':
			var ok bool
			if r, p, ok = decodeJSONUnicodeEscape(s, p); !ok {
				return "", nil, false
			}
		default:
			return "", nil, false
		}
		var buf [utf8.UTFMax]byte
		for _, x := range buf[:utf8.EncodeRune(buf[:], r)] {
			b.WriteByte(x)
			pos = append(pos, at)
		}
	}
	return "", nil, false
}

// decodeJSONUnicodeEscape reads \uXXXX at p, with its low surrogate when it
// is a high one, and returns the rune and where it ends.
func decodeJSONUnicodeEscape(s string, p int) (rune, int, bool) {
	hex := func(at int) (rune, bool) {
		if at+6 > len(s) || s[at] != '\\' || s[at+1] != 'u' {
			return 0, false
		}
		var v rune
		for _, c := range s[at+2 : at+6] {
			switch {
			case c >= '0' && c <= '9':
				v = v<<4 | (c - '0')
			case c >= 'a' && c <= 'f':
				v = v<<4 | (c - 'a' + 10)
			case c >= 'A' && c <= 'F':
				v = v<<4 | (c - 'A' + 10)
			default:
				return 0, false
			}
		}
		return v, true
	}
	r, ok := hex(p)
	if !ok {
		return 0, 0, false
	}
	if utf16.IsSurrogate(r) {
		if lo, ok := hex(p + 6); ok {
			if d := utf16.DecodeRune(r, lo); d != utf8.RuneError {
				return d, p + 12, true
			}
		}
		return utf8.RuneError, p + 6, true
	}
	return r, p + 6, true
}
