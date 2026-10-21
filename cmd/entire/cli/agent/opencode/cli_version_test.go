package opencode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/paths"
)

// useOpenCodeCLI pins the detected OpenCode version for one test.
func useOpenCodeCLI(t *testing.T, legacy bool) {
	t.Helper()
	prev := detectOpenCodeCLI
	detectOpenCodeCLI = func(context.Context) openCodeCLI { return openCodeCLI{legacy: legacy} }
	t.Cleanup(func() { detectOpenCodeCLI = prev })
}

// stubOpenCode puts a shell-script `opencode` first on PATH.
func stubOpenCode(t *testing.T, script string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub opencode is a shell script")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+"/usr/bin:/bin")
}

func TestProbeOpenCodeCLI(t *testing.T) {
	// No t.Parallel: t.Setenv.
	for _, tt := range []struct {
		name   string
		script string
		legacy bool
	}{
		{"1.x", "echo 1.18.33\n", true},
		{"2.x", "echo 'opencode v2.1.1'\n", false},
		{"unreadable version", "echo nightly\n", false},
		{"failing probe", "exit 3\n", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			stubOpenCode(t, tt.script)
			cli, cacheable := probeOpenCodeCLI(t.Context())
			if cli.legacy != tt.legacy {
				t.Fatalf("probe = %+v, want legacy=%v", cli, tt.legacy)
			}
			if want := tt.name != "failing probe"; cacheable != want {
				t.Fatalf("cacheable = %v, want %v", cacheable, want)
			}
		})
	}
}

func TestOpenCodeCLIExportArgs(t *testing.T) {
	t.Parallel()
	if got := (openCodeCLI{legacy: true}).exportArgs("ses_1"); !slices.Equal(got, []string{"export", "ses_1"}) {
		t.Errorf("1.x export args = %v", got)
	}
	// Without --standalone, OpenCode 2 attaches to (or starts) the shared
	// background service, which then outlives the hook.
	if got := (openCodeCLI{}).exportArgs("ses_1"); !slices.Equal(got, []string{"session", "export", "--standalone", "ses_1"}) {
		t.Errorf("2.x export args = %v", got)
	}
}

// OpenCode 2 exports with `session export --standalone`.
func TestRunOpenCodeExportToFile_OpenCode2(t *testing.T) {
	// No t.Parallel: t.Setenv.
	useOpenCodeCLI(t, false)
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	stubOpenCode(t, `echo "$@" > `+argsFile+"\n"+`cat "`+filepath.Join(dir, "export.json")+`"`+"\n")
	if err := os.WriteFile(filepath.Join(dir, "export.json"), loadOpenCode2Fixture(t, "tools"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := mustOpenRoot(t, dir)
	if err := runOpenCodeExportToFile(t.Context(), root, "ses_x", "staged"); err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(args)) != "session export --standalone ses_x" {
		t.Errorf("opencode args = %q", args)
	}
}

func TestReportsSessionNotFound(t *testing.T) {
	t.Parallel()
	if !reportsSessionNotFound("Session not found: ses_abc\n") {
		t.Error("OpenCode 2 missing-session line not recognized")
	}
	if reportsSessionNotFound("Error: Failed to import session: Bad Request\n") {
		t.Error("unrelated error treated as a missing session")
	}
}

// On OpenCode 2, resuming never deletes the session: the stored transcript
// is in the 1.x shape, which OpenCode 2 cannot import back.
func TestWriteSession_OpenCode2NeverDeletes(t *testing.T) {
	// No t.Parallel: t.Setenv.
	useOpenCodeCLI(t, false)
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	session := `{"info":{"id":"ses_r"},"messages":[]}`

	t.Run("present", func(t *testing.T) {
		stubOpenCode(t, `echo "$@" >> `+log+"\nprintf '%s' '"+session+"'\n")
		ag := &OpenCodeAgent{}
		err := ag.WriteSession(t.Context(), &agent.AgentSession{SessionID: "ses_r", NativeData: []byte(session)})
		if err != nil {
			t.Fatalf("WriteSession: %v", err)
		}
	})
	t.Run("missing", func(t *testing.T) {
		stubOpenCode(t, `echo "$@" >> `+log+"\necho 'Session not found: ses_r' >&2; exit 1\n")
		ag := &OpenCodeAgent{}
		err := ag.WriteSession(t.Context(), &agent.AgentSession{SessionID: "ses_r", NativeData: []byte(session)})
		if err == nil || !strings.Contains(err.Error(), "cannot import") {
			t.Fatalf("WriteSession error = %v, want a clear cannot-import error", err)
		}
	})

	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(calls)), "\n") {
		if line != "session export --standalone ses_r" {
			t.Errorf("unexpected opencode call %q (only exports are allowed on OpenCode 2)", line)
		}
	}
}

// The capture stores an OpenCode 2 export in the 1.x shape the rest of Entire
// reads.
func TestFetchAndCacheExport_NormalizesOpenCode2(t *testing.T) {
	// Not parallel: t.Chdir and stubExport.
	fixture := loadOpenCode2Fixture(t, "tools") // before t.Chdir: testdata is relative
	t.Chdir(t.TempDir())
	paths.ClearWorktreeRootCache()
	t.Cleanup(paths.ClearWorktreeRootCache)
	stubExport(t, func(_ context.Context, root *os.Root, _, outputName string) error {
		return root.WriteFile(outputName, fixture, 0o600)
	})

	path, err := (&OpenCodeAgent{}).fetchAndCacheExport(t.Context(), "ses_v2")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if isOpenCodeV2Export(data) || !json.Valid(data) {
		t.Fatalf("cached transcript is not a normalized export: %.200s", data)
	}
	prompts, err := ExtractAllUserPrompts(data)
	if err != nil || len(prompts) != 1 {
		t.Fatalf("prompts = %q, err %v", prompts, err)
	}
}
