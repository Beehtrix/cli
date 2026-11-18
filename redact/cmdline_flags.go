package redact

import "strings"

// The flag table: which programs take a password on their command line, and
// how to find the command that a wrapper or runner starts.

// cmdClient is how one client takes its password.
type cmdClient struct {
	// glued are short flags whose value only follows in the same word
	// (mysql's -pSecret; a bare -p prompts).
	glued string
	// short are short flags whose value is glued or the next word.
	short string
	// cluster are short options that take no value and may come before a
	// password flag in one word (mysql -fpSecret, sshpass -vp Secret).
	cluster string
	// required means a short flag always takes a value, even one that
	// starts with a dash (sshpass -p -Secret).
	required bool
	// login means the flags count only after a login subcommand.
	login bool
	// stopAtOperand ends the client's options at its first operand, as
	// sshpass's own options end at the command it runs.
	stopAtOperand bool
	// passSource reads openssl's pass:Secret password sources.
	passSource bool
	// noValueLong means --password takes its value only after "=": a blank
	// --password prompts (mysql, psql).
	noValueLong bool
	// skip are short options whose value is the next word.
	skip string
}

var (
	mysqlClient     = cmdClient{glued: "p", cluster: "ABCEGHINTUVXbcfinqrstvw", noValueLong: true}
	mysqlToolClient = cmdClient{glued: "p", noValueLong: true}
	mysqldumpClient = cmdClient{glued: "p", cluster: "ABCEFKQRYacdeflnqtvx", noValueLong: true}
	loginClient     = cmdClient{short: "p", required: true, login: true}
	mongoClient     = cmdClient{short: "p"}
	sshpassClient   = cmdClient{short: "p", cluster: "vhV", required: true, stopAtOperand: true, skip: "fdP"}
	redisClient     = cmdClient{short: "a", required: true}
	psqlClient      = cmdClient{noValueLong: true}
	opensslClient   = cmdClient{passSource: true}
)

var cmdClients = map[string]cmdClient{
	"mysql": mysqlClient, "mariadb": mysqlClient, "mysqldump": mysqldumpClient, "mariadb-dump": mysqldumpClient,
	"docker": loginClient, cmdPodman: loginClient, "nerdctl": loginClient, "buildah": loginClient,
	"skopeo": loginClient, "oras": loginClient, "helm": loginClient, "az": loginClient,
	"mongo": mongoClient, "mongosh": mongoClient, "mongodump": mongoClient, "mongorestore": mongoClient,
	"mongoexport": mongoClient, "mongoimport": mongoClient, "mongostat": mongoClient, "mongotop": mongoClient,
	"mongofiles": mongoClient,
	"sshpass":    sshpassClient,
	"redis-cli":  redisClient,
	"psql":       psqlClient, "pgcli": psqlClient, "createdb": psqlClient, "dropdb": psqlClient,
	"createuser": psqlClient, "dropuser": psqlClient, "vacuumdb": psqlClient, "reindexdb": psqlClient,
	"clusterdb": psqlClient,
	"openssl":   opensslClient,
	"wget":      {},
}

// clientFor returns the client a program name names. Every mysql and
// mariadb tool takes the same glued -p, and every PostgreSQL tool's
// --password prompts.
func clientFor(name string) (cmdClient, bool) {
	if cl, ok := cmdClients[name]; ok {
		return cl, true
	}
	if !isToolName(name) {
		return cmdClient{}, false
	}
	switch {
	case strings.HasPrefix(name, "mysql"), strings.HasPrefix(name, "mariadb"):
		return mysqlToolClient, true
	case strings.HasPrefix(name, "pg_"):
		return psqlClient, true
	}
	return cmdClient{}, false
}

// isToolName reports whether name is spelled like a program: mysqldump,
// mariadb-backup, pg_dump.
func isToolName(name string) bool {
	for i := range len(name) {
		if c := name[i]; (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return name != ""
}

// textPrograms print or search their arguments, so a flag among them is
// text: grep -- "--password" docs/.
var textPrograms = map[string]bool{
	"echo": true, "printf": true, "grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true,
	"ack": true, "sed": true, "awk": true, "gawk": true,
}

// programName is a program word's name: its base name in either path
// spelling, lowercased, without .exe.
func programName(t string) string {
	if i := strings.LastIndexAny(t, `/\`); i >= 0 {
		t = t[i+1:]
	}
	return strings.TrimSuffix(strings.ToLower(t), ".exe")
}

// cmdWrappers run the rest of their command line after their own options.
// The value lists the short options that take the next word.
var cmdWrappers = map[string]string{
	"sudo": "ughpCDrtUT", "doas": "uC", "env": "uC", "nice": "n", "ionice": "cnp", "nohup": "",
	"time": "fo", "timeout": "sk", "exec": "a", "command": "", "builtin": "", "stdbuf": "ioe",
	"xargs": "IinPLdEsa", "watch": "nd", "unbuffer": "", "caffeinate": "tw", "chronic": "",
	"then": "", "do": "", "else": "", "if": "", "elif": "", "while": "", "until": "", "!": "", "{": "",
}

// cmdWrapperLongValues are the wrappers' long options that take the next word.
var cmdWrapperLongValues = map[string]bool{
	"--user": true, "--group": true, "--host": true, "--prompt": true, "--chdir": true, "--role": true,
	"--type": true, "--other-user": true, "--close-from": true, "--unset": true,
	"--signal": true, "--kill-after": true, "--adjustment": true, "--max-args": true, "--max-procs": true,
	"--replace": true, "--delimiter": true, "--arg-file": true, "--interval": true,
}

// programIndex is the index of the word that names the program of the
// command starting at words[from], past assignments and wrappers, or -1.
func programIndex(words []cmdWord, from int) int {
	i := from
	if len(words) > i+1 && len(words[i].text) == 1 && strings.IndexByte("$>%#", words[i].text[0]) >= 0 {
		i++ // a shell prompt in documentation
	}
	for i < len(words) {
		t := words[i].text
		if eq := strings.IndexByte(t, '='); eq > 0 && !words[i].leadQuoted && isShellName(t[:eq]) {
			i++
			continue
		}
		valueOpts, ok := cmdWrappers[programName(t)]
		if !ok {
			return i
		}
		i++
		for i < len(words) && strings.HasPrefix(words[i].text, "-") {
			opt := words[i].text
			i++
			if len(opt) == 2 && strings.IndexByte(valueOpts, opt[1]) >= 0 || cmdWrapperLongValues[opt] {
				i++
			}
		}
		// timeout's duration and nice's adjustment come before the command.
		for i < len(words) && isDigits(strings.TrimRight(words[i].text, "smhd.")) {
			i++
		}
	}
	return -1
}

// subCommandStart returns where the command a runner runs starts: after
// ssh's host, docker exec's container or docker run's image, kubectl exec's
// "--", find's -exec, or sshpass's own options. -1 when there is none.
func subCommandStart(words []cmdWord, prog int, name string) int {
	switch name {
	case "ssh", "autossh":
		// ssh reads options after its host too.
		const values = "BbcDEeFIiJLlmOoPpQRSWw"
		start := afterOperand(words, skipOptions(words, prog+1, values, false, nil))
		if start < 0 {
			return -1
		}
		return skipOptions(words, start, values, false, nil)
	case "docker", cmdPodman, "nerdctl":
		for i := prog + 1; i < len(words); i++ {
			if t := words[i].text; t == "exec" || t == "run" {
				return containerCommandStart(words, i+1, name == cmdPodman && t == "exec")
			}
		}
	case "kubectl", "oc":
		for i := prog + 1; i < len(words); i++ {
			if words[i].text == "--" {
				return i + 1
			}
		}
	case "find":
		for i := prog + 1; i < len(words); i++ {
			switch words[i].text {
			case "-exec", "-execdir", "-ok", "-okdir":
				return i + 1
			}
		}
	case "sshpass":
		return skipOptions(words, prog+1, "fdPp", false, nil)
	case "eval":
		return afterOperand(words, prog)
	}
	return -1
}

// containerCommandStart returns where the command run in a container starts,
// after the options from words[i] and the container. podman exec --latest
// (-l) names no container, so there the command follows the options.
func containerCommandStart(words []cmdWord, i int, latestOK bool) int {
	var latest bool
	seen := &latest
	if !latestOK {
		seen = nil
	}
	end := skipOptions(words, i, "", true, seen)
	if latest {
		if end < len(words) {
			return end
		}
		return -1
	}
	return afterOperand(words, end)
}

// skipOptions skips the options from words[i]: a short option listed in
// values takes the next word, and with longValues every option without "="
// does, except a few common switches (docker run -it --rm). With latest set,
// -l and --latest are switches too, and seeing one sets *latest.
func skipOptions(words []cmdWord, i int, values string, longValues bool, latest *bool) int {
	for i < len(words) {
		t := words[i].text
		if t == "--" {
			return i + 1
		}
		if len(t) < 2 || t[0] != '-' {
			return i
		}
		i++
		switch {
		case strings.Contains(t, "="):
		case t[1] != '-':
			if shortTakesNext(t, values, longValues, latest) {
				i++
			}
		case latest != nil && t == "--latest":
			*latest = true
		case longValues && !isContainerSwitch(t):
			i++
		}
	}
	return i
}

// containerSwitches are the short container options that take no value.
const containerSwitches = "ditqP"

// shortTakesNext reports whether the short option cluster t takes the next
// word as its value: the first of its letters that takes a value is its last
// (-vp takes it; -p2222 and -w/tmp hold their own).
func shortTakesNext(t, values string, longValues bool, latest *bool) bool {
	for j := 1; j < len(t); j++ {
		c := t[j]
		if latest != nil && c == 'l' {
			*latest = true
			continue
		}
		if strings.IndexByte(values, c) >= 0 || longValues && strings.IndexByte(containerSwitches, c) < 0 {
			return j == len(t)-1
		}
	}
	return false
}

func isContainerSwitch(t string) bool {
	switch t {
	case "--rm", "--detach", "--interactive", "--tty", "--privileged", "--init", "--read-only", "--quiet",
		"--publish-all", "--no-healthcheck", "--sig-proxy", "--oom-kill-disable":
		return true
	}
	return false
}

// afterOperand returns the index after the operand at i, or -1.
func afterOperand(words []cmdWord, i int) int {
	if i < 0 || i >= len(words)-1 {
		return -1
	}
	return i + 1
}

func isShellName(s string) bool {
	for i := range len(s) {
		c := s[i]
		if c != '_' && (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return s != ""
}

func isDigits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return s != ""
}

// commandHits finds the password values in one simple command.
//
// A client named where a command starts makes the words a command even in
// text read with care, so its long flags take the word after them as the
// value, as in a command field.
func commandHits(words []cmdWord, careful bool) []cmdHit {
	var hits []cmdHit
	prog, sure := -1, false
	for depth, from := 0, 0; depth <= cmdMaxDepth && from >= 0 && from < len(words); depth++ {
		p := programIndex(words, from)
		if p < 0 {
			break
		}
		if prog < 0 {
			prog = p
		}
		name := programName(words[p].text)
		if w := words[p]; from > 0 && w.quoted && !strings.HasPrefix(w.text, "-") {
			// ssh and eval join their words into one command, so a
			// quoted "sudo mysql" names the program of what follows.
			if f := strings.Fields(w.text); len(f) > 1 {
				name = programName(f[len(f)-1])
			}
		}
		if cl, ok := clientFor(name); ok {
			sure = true
			hits = append(hits, clientHits(words, p, cl, careful)...)
			if cl.passSource || cl.noValueLong {
				return hits // its long flags are read with it
			}
		}
		from = subCommandStart(words, p, name)
	}
	if prog >= 0 && textPrograms[programName(words[prog].text)] {
		return hits
	}
	return append(hits, longFlagHits(words, 0, careful && !sure, false)...)
}

// clientHits reads the flags of the client named by words[c].
func clientHits(words []cmdWord, c int, cl cmdClient, careful bool) []cmdHit {
	if cl.passSource {
		return passSourceHits(words, c)
	}
	var hits []cmdHit
	if cl.noValueLong {
		hits = longFlagHits(words, c+1, careful, true)
	}
	sawLogin := !cl.login
	for i := c + 1; i < len(words); i++ {
		t := words[i].text
		switch {
		case !sawLogin:
			sawLogin = t == "login" && isLoginSubcommand(words, i)
		case len(t) == 2 && t[0] == '-' && strings.IndexByte(cl.skip, t[1]) >= 0:
			i++ // sshpass -P 'Password:'
		case len(t) < 2 || t[0] != '-' || t[1] == '-':
			if cl.stopAtOperand && len(t) > 0 && t[0] != '-' {
				return hits
			}
		default:
			h, ok, used := shortFlagHit(words, i, cl, careful)
			if ok {
				hits = append(hits, h)
			}
			i += used
		}
	}
	return hits
}

// passSourceHits reads openssl's password sources: -pass pass:Secret and
// -passin=pass:Secret. The env:, file: and fd: sources only name where the
// password is.
func passSourceHits(words []cmdWord, c int) []cmdHit {
	var hits []cmdHit
	for i := c + 1; i < len(words); i++ {
		w := words[i]
		at := strings.Index(w.text, "pass:")
		if (at == 0 || at > 0 && w.text[at-1] == '=' && w.text[0] == '-') && isCommandLineSecret(w.text[at+5:], w.literalFrom(at+5)) {
			hits = append(hits, cmdHit{word: i, at: at + 5})
		}
	}
	return hits
}

// shortFlagHit reads the short option word at i: a password glued to it or
// in the next word. used is 1 when the next word was read as its value.
func shortFlagHit(words []cmdWord, i int, cl cmdClient, careful bool) (h cmdHit, ok bool, used int) {
	w := words[i]
	if careful && w.leadQuoted && !w.wholeQuoted {
		return cmdHit{}, false, 0 // "-p"+password is code that builds the flag
	}
	at, next := shortFlagValue(w.text, cl)
	if at > 0 {
		v := w.text[at:]
		return cmdHit{word: i, at: at}, isCommandLineSecret(v, w.literalFrom(at)) && (!careful || !isMetavar(v)), 0
	}
	if !next || i+1 >= len(words) {
		return cmdHit{}, false, 0
	}
	v := words[i+1]
	if strings.HasPrefix(v.text, "-") && (!cl.required || isOptionLike(v.text)) {
		return cmdHit{}, false, 0
	}
	return cmdHit{word: i + 1}, isCommandLineSecret(v.text, v.literalFrom(0)) && (!careful || !isMetavar(v.text)), 1
}

// isLoginSubcommand reports whether "login" at i is a subcommand rather than
// an option's value (docker run --name login).
func isLoginSubcommand(words []cmdWord, i int) bool {
	prev := words[i-1].text
	if !strings.HasPrefix(prev, "-") || strings.Contains(prev, "=") {
		return true
	}
	switch prev {
	case "--debug", "-D", "--tls", "--tlsverify", "--insecure", "--verbose":
		return true
	}
	return false
}

// shortFlagValue reads a short option word: at > 0 is where a glued value
// starts, next means the value is the next word.
func shortFlagValue(t string, cl cmdClient) (at int, next bool) {
	for k := 1; k < len(t); k++ {
		c := t[k]
		switch {
		case strings.IndexByte(cl.glued, c) >= 0:
			if k+1 < len(t) {
				return k + 1, false
			}
			return 0, false
		case strings.IndexByte(cl.short, c) >= 0:
			if k+1 < len(t) {
				return k + 1, false
			}
			return 0, true
		case strings.IndexByte(cl.cluster, c) < 0:
			return 0, false
		}
	}
	return 0, false
}

// longFlagHits reads --password, --passwd and --pass from words[from:].
// noBlankValue skips the blank form, which a client reads as no value.
func longFlagHits(words []cmdWord, from int, careful, noBlankValue bool) []cmdHit {
	var hits []cmdHit
	for i := from; i < len(words); i++ {
		w := words[i]
		name, value, hasEq := strings.Cut(w.text, "=")
		if name != "--password" && name != "--passwd" && name != "--pass" {
			continue
		}
		if hasEq {
			if isCommandLineSecret(value, w.literalFrom(len(name)+1)) {
				hits = append(hits, cmdHit{word: i, at: len(name) + 1})
			}
			continue
		}
		if noBlankValue || i+1 >= len(words) {
			continue
		}
		v := words[i+1]
		if strings.HasPrefix(v.text, "-") && (careful || isOptionLike(v.text)) {
			continue
		}
		if careful && (w.quoted || !v.quoted && readsAsProse(v.text)) || name == "--pass" && len(v.text) == 1 && isDigits(v.text) {
			continue // x264 --pass 2
		}
		if isCommandLineSecret(v.text, v.literalFrom(0)) {
			hits = append(hits, cmdHit{word: i + 1})
		}
	}
	return hits
}

// isOptionLike reports whether a word that follows a password flag is the
// next option rather than a value: -u, --host, --host=db.
func isOptionLike(v string) bool {
	if len(v) == 2 {
		return v[0] == '-' && (v[1] >= 'a' && v[1] <= 'z' || v[1] >= 'A' && v[1] <= 'Z')
	}
	name, _, _ := strings.Cut(v, "=")
	return strings.HasPrefix(name, "--") && len(name) > 3 && isToolName(name[2:]) && name[2] >= 'a' && name[2] <= 'z'
}

// readsAsProse reports whether the word after a blank long flag, in text
// that may not be a command, reads as something other than a password: a
// prose word or phrase, a metavar, a pass number or a bracketed name.
func readsAsProse(v string) bool {
	v = strings.TrimRight(v, ".,;:!?)")
	if v == "" || len(v) == 1 && isDigits(v) || v[0] == '<' || v[0] == '[' {
		return true
	}
	if first, _, phrase := strings.Cut(v, " "); phrase {
		v = first // "is required", "for the database"
	}
	lower := v != ""
	for i := range len(v) {
		lower = lower && v[i] >= 'a' && v[i] <= 'z'
	}
	return lower || isMetavar(v)
}

// isMetavar reports whether v is written like a metavar: PASSWORD, DB_PASS.
func isMetavar(v string) bool {
	if len(v) < 2 || v[0] < 'A' || v[0] > 'Z' {
		return false
	}
	for i := range len(v) {
		c := v[i]
		if (c < 'A' || c > 'Z') && c != '_' && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// isCommandLineSecret reports whether a flag value can be a password rather
// than a reference to one.
func isCommandLineSecret(v string, literal bool) bool {
	if v == "" || strings.HasPrefix(v, "{{") || isFormatPlaceholder(v) {
		return false
	}
	if len(v) <= 2 && !isAlnum(v[0]) && !isAlnum(v[len(v)-1]) {
		return false // "-p" + password
	}
	if !literal {
		if isOpenReference(v) {
			return false
		}
		if len(v) > 2 && v[0] == '`' && v[len(v)-1] == '`' {
			v = "$(" + v[1:len(v)-1] + ")" // `cat /run/pw` is a recipe like $(cat /run/pw)
		}
	}
	return !isPlaceholderSecretValueQuoted(v, literal)
}

// isFormatPlaceholder reports whether v is a format verb or template slot
// that code fills in: %s, %v, %(pw)s, {}, {0}, {password}.
func isFormatPlaceholder(v string) bool {
	switch {
	case len(v) == 2 && v[0] == '%':
		return true
	case strings.HasPrefix(v, "%(") && strings.HasSuffix(v, "s"):
		return true
	case len(v) >= 2 && v[0] == '{' && v[len(v)-1] == '}':
		return !strings.ContainsAny(v[1:len(v)-1], "{} ")
	}
	return false
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// isOpenReference reports whether v starts a substitution or expansion that
// the reader never saw close, which only fetches the value.
func isOpenReference(v string) bool {
	switch {
	case strings.HasPrefix(v, "$("):
		return !strings.HasSuffix(v, ")")
	case strings.HasPrefix(v, "${"):
		return !strings.HasSuffix(v, "}")
	case strings.HasPrefix(v, "`"):
		return len(v) < 2 || !strings.HasSuffix(v, "`")
	}
	return false
}
