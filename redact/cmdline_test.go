package redact

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The shapes issue #2898 reports: a password typed on a command line, far
// below the entropy threshold.
func TestString_CommandLinePasswords(t *testing.T) {
	t.Parallel()
	assertStringRedactionCases(t, []stringRedactionCase{
		{"mysql glued -p", "mysql -u root -pSummer2026Release mydb", "mysql -u root -pREDACTED mydb"},
		{"docker login -p", "docker login -u me -p Summer2026Release ghcr.io", "docker login -u me -p REDACTED ghcr.io"},
		{"sshpass -p", "sshpass -p Summer2026Release ssh user@host", "sshpass -p REDACTED ssh user@host"},
		{"redis-cli -a", "redis-cli -a Summer2026Release", "redis-cli -a REDACTED"},
		{"mongosh --password", "mongosh --password Summer2026Release", "mongosh --password REDACTED"},
		{"az login -p", "az login -p Summer2026Release", "az login -p REDACTED"},
		{"openssl pass source", "openssl enc -aes-256-cbc -pass pass:Summer2026Release", "openssl enc -aes-256-cbc -pass pass:REDACTED"},
		{"netrc entry", "machine host login me password Summer2026Release", "machine host login me password REDACTED"},
		{"netrc written by printf", `printf "machine h\nlogin me\npassword Summer2026Release\n" > ~/.netrc`, `printf "machine h\nlogin me\npassword REDACTED\n" > ~/.netrc`},
		{"netrc file without login", "machine db.example.com\n  password Summer2026Release\n", "machine db.example.com\n  password REDACTED\n"},
		{"netrc password with shell operators", "machine h login me password rainy;summer&x\n", "machine h login me password REDACTED\n"},
		{"netrc quoted password with an escaped quote", `machine h login me password "Summer\"2026"`, `machine h login me password "REDACTED"`},
		{"single-quoted value", "mysql -u root -p'Summer 2026' mydb", "mysql -u root -p'REDACTED' mydb"},
		{"single-quoted reference is literal", "mysql -u root -p'$PASSWORD' mydb", "mysql -u root -p'REDACTED' mydb"},
		{"escaped dollar is literal", `mysql -p\$ecret1 db`, `mysql -p\REDACTED db`},
		{"escaped dollar in double quotes is literal", `mysql -p"\$ecret1" db`, `mysql -p"\REDACTED" db`},
		{"option cluster", "mysql -fpSummer2026Release", "mysql -fpREDACTED"},
		{"through a wrapper", "sudo -u mysql mysql -pSummer2026Release", "sudo -u mysql mysql -pREDACTED"},
		{"in a container", "docker exec -it mongo mongosh -u root -p Summer2026Release", "docker exec -it mongo mongosh -u root -p REDACTED"},
		{"podman exec --latest names no container", "podman exec -it -l mysql -pSummer2026Release", "podman exec -it -l mysql -pREDACTED"},
		{"docker run -l is a label", "docker run -l app=db mysql:8 mysql -pSummer2026Release", "docker run -l app=db mysql:8 mysql -pREDACTED"},
		{"container option with its value attached", "docker exec -w/tmp box redis-cli -a Summer2026Release", "docker exec -w/tmp box redis-cli -a REDACTED"},
		{"sshpass option cluster", "sshpass -vp Summer2026Release ssh host redis-cli -a swordfish", "sshpass -vp REDACTED ssh host redis-cli -a REDACTED"},
		{"after a closed descriptor", "exec 2>&-; redis-cli -a Summer2026Release", "exec 2>&-; redis-cli -a REDACTED"},
		{"after a closed descriptor and &&", "make >&- && mysql -pSummer2026Release db", "make >&- && mysql -pREDACTED db"},
		{"in sh -c", `sh -c "mysql -pSummer2026Release"; echo " done"`, `sh -c "mysql -pREDACTED"; echo " done"`},
		{"over ssh", `ssh db 'redis-cli -a Summer2026Release ping'`, `ssh db 'redis-cli -a REDACTED ping'`},
		{"in a substitution", "out=$(mysql -u root -pSummer2026Release)", "out=$(mysql -u root -pREDACTED)"},
		{"in backquotes in prose", "Run `docker login -u me -p Summer2026Release` first.", "Run `docker login -u me -p REDACTED` first."},
		{"long flag with =", "wget --password=Summer2026Release https://example.com", "wget --password=REDACTED https://example.com"},
		{"client long flag", "mongosh --password swordfish --host db", "mongosh --password REDACTED --host db"},
		{"code argument list", `subprocess.run(["mysql", "-u", "root", "-pSummer2026Release"])`, `subprocess.run(["mysql", "-u", "root", "-pREDACTED"])`},
		{"code argument list with a variable", `exec.Command("docker", "login", "-u", user, "-p", "Summer2026Release")`, `exec.Command("docker", "login", "-u", user, "-p", "REDACTED")`},
		{"multi-line code list", "exec.Command(\n\t\"wget\",\n\t\"--password\", // set below\n\t\"Summer2026Release\",\n)", "exec.Command(\n\t\"wget\",\n\t\"--password\", // set below\n\t\"REDACTED\",\n)"},
	})
}

// Text that only looks like one: options of other programs, documentation
// and prose, references to a password, and code that builds the flag.
func TestString_CommandLinePasswordsStayVisible(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		"find /etc/mysql -type f -print",
		`grep -rn -- "--password" docs/`,
		"error: --password requires a value",
		"Set the --password flag when the server asks.",
		"  --password string   Password for the registry",
		"Enter machine name, login and password below.",
		"machine learning password policy",
		"machine example.com password is stored in the vault",
		"go test ./mysql -parallel 8",
		"tar -C /opt/mysql -pxzf a.tgz",
		"nmap mysql -p3306",
		"ssh -p 2222 mysql.example.com",
		"docker run --name mysql -p 3306:3306 mysql:8",
		"mysql -u root -p mydb",
		"mysql -u root -p$PASSWORD mydb",
		"mysql -uUSER -pPASSWORD db",
		`mysql -p"$(cat /run/secrets/db)" app`,
		"wget --password=`cat /run/pw` https://example.com",
		"psql --password -h db -U app app",
		"pg_dump --password -h db -U app app",
		"mongosh --password -u admin",
		"x264 --pass 2 -o out.mkv in.y4m",
		"openssl rsa -in k.pem -passin env:KEY_PASS",
		"openssl rsa -in k.pem -passin file:/run/pass",
		"mysqlslap -qpurge -uphil",
		`fmt.Sprintf("mysql -p%s", pw)`,
		`fmt.Sprintf("machine %s login %s password %s", host, user, pass)`,
		`netrc := "machine h login me password " + password + "\n"`,
		"type netrcLine struct {\n\tmachine  string\n\tlogin    string\n\tpassword string\n}",
		`exec.Command("mysql", "-p"+password)`,
		`print("--password", "is required")`,
		`parser.add_argument("--password", "-p", help="database password")`,
		`flags := []string{"--password", "--help"}`,
		`subprocess.run(["redis-cli", "-a", "$PASSWORD"])`,
		`sh -c "mysql -p\$PASSWORD"`,
		`mysql -p"$PASSWORD" db`,
	} {
		require.Equal(t, in, String(in), in)
	}
}

// A command an agent ran is a command, so the word after a password flag is
// its value even where text read with care would not take it.
func TestJSONLContent_CommandFieldPasswords(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		// Claude Code, Gemini CLI and OpenCode keep the command string.
		{`{"type":"tool_use","name":"Bash","input":{"command":"tool --password hunter2"}}`,
			`{"type":"tool_use","name":"Bash","input":{"command":"tool --password REDACTED"}}`},
		{`{"type":"tool_use","name":"Bash","input":{"command":"wget --password -Secret1 https://example.com"}}`,
			`{"type":"tool_use","name":"Bash","input":{"command":"wget --password REDACTED https://example.com"}}`},
		// A blank --password still prompts for mysql and psql.
		{`{"type":"tool_use","name":"Bash","input":{"command":"psql --password production"}}`,
			`{"type":"tool_use","name":"Bash","input":{"command":"psql --password production"}}`},
		// Codex keeps argument lists.
		{`{"type":"CommandExecution","command":["mysql","-phunter2"]}`,
			`{"type":"CommandExecution","command":["mysql","-pREDACTED"]}`},
		{`{"type":"CommandExecution","command":["docker","login","-u","me","-p","hunter2","ghcr.io"]}`,
			`{"type":"CommandExecution","command":["docker","login","-u","me","-p","REDACTED","ghcr.io"]}`},
		{`{"type":"CommandExecution","command":["/bin/zsh","-lc","mysql -phunter2 app"]}`,
			`{"type":"CommandExecution","command":["/bin/zsh","-lc","mysql -pREDACTED app"]}`},
		{`{"command":["echo","machine","h","login","me","password","hunter2"]}`,
			`{"command":["echo","machine","h","login","me","password","REDACTED"]}`},
		// and writes a tool call's arguments as a JSON string.
		{`{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"mysql -uroot -phunter2 -e \\\"select 1\\\"\",\"workdir\":\"/repo\"}"}`,
			`{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"mysql -uroot -pREDACTED -e \\\"select 1\\\"\",\"workdir\":\"/repo\"}"}`},
		{`{"type":"function_call","name":"shell","arguments":"{\"command\":[\"bash\",\"-lc\",\"redis-cli -a hunter2 ping\"]}"}`,
			`{"type":"function_call","name":"shell","arguments":"{\"command\":[\"bash\",\"-lc\",\"redis-cli -a REDACTED ping\"]}"}`},
		{`{"type":"function_call","name":"shell","arguments":"{\"command\":[\"sshpass\",\"-p\",\"hunter2\",\"ssh\",\"h\"]}"}`,
			`{"type":"function_call","name":"shell","arguments":"{\"command\":[\"sshpass\",\"-p\",\"REDACTED\",\"ssh\",\"h\"]}"}`},
		// A password the list alone gives away leaves an equal object key.
		{`{"type":"x","command":["redis-cli","-a","type"]}`,
			`{"type":"x","command":["redis-cli","-a","REDACTED"]}`},
		// Lists that are no command, or name no password, stay.
		{`{"command":["mysql","-p","app"]}`, `{"command":["mysql","-p","app"]}`},
		{`{"command":["docker","run","-p","3306:3306","mysql:8"]}`, `{"command":["docker","run","-p","3306:3306","mysql:8"]}`},
		{`{"items":["mysql","-pSecret"]}`, `{"items":["mysql","-pSecret"]}`},
		{`{"flags":["--password","--help"]}`, `{"flags":["--password","--help"]}`},
	} {
		got, err := JSONLContent(tc.in)
		require.NoError(t, err, tc.in)
		require.Equal(t, tc.want, got, tc.in)
		bytes, err := JSONLBytes([]byte(tc.in + "\n"))
		require.NoError(t, err, tc.in)
		require.Equal(t, tc.want+"\n", string(bytes.Bytes()), tc.in)
	}
}

// Every reader moves forward only: an unclosed quote, bracket or substitution
// costs one step, not a scan to the end of the text each time it is met, and
// a quoted command is read again at most cmdMaxDepth deep. A quadratic shape
// at this size would not finish within the test timeout.
func TestCommandLinePasswordsStayLinear(t *testing.T) {
	t.Parallel()
	const n = 1 << 20
	for _, unit := range []string{
		"mysql -p$( ", "mysql -p\"$(printf x", "sh -c 'mysql -pX ", "-p$( -p${ <( `",
		`["mysql", "-pX"], `, `"--password", `, `exec.Command("mysql", x(`,
		"machine h login u password p ", "machine \"h login\" password '",
		"ssh db \"nice mysql\" ", "docker run --entrypoint x ", "sudo -u ",
		"mysql -p '\\", "mysql -p \\\"", `"mysql", 'x, `,
	} {
		in := strings.Repeat(unit, n/len(unit))
		_ = detectCommandLinePasswords(in)
		_ = commandRegions(in)
	}
	in := strings.Repeat("mysql -pSecret1; ", n/17)
	require.Equal(t, n/17, strings.Count(String(in), "-pREDACTED"))
}

// Every region either reader returns lies inside the text, whatever the text.
func FuzzCommandLinePasswords(f *testing.F) {
	for _, seed := range []string{
		"mysql -u root -pSecret1 db", `sh -c "mysql -p'x' && echo \"$(redis-cli -a y)\""`,
		"machine h login u password p", "printf \"machine h\\npassword p\" > ~/.netrc",
		`exec.Command("docker", "login", "-p", "x")`, "podman exec -l mysql -px",
		"mysql -p$( ", ")'sshpass ", "psql${ \"", "mysql -e $(echo `x`",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for _, regions := range [][]taggedRegion{detectCommandLinePasswords(s), commandRegions(s)} {
			for _, r := range regions {
				if r.start < 0 || r.end > len(s) || r.start >= r.end {
					t.Fatalf("region [%d,%d) outside %q", r.start, r.end, s)
				}
			}
		}
		_ = String(s)
	})
}
