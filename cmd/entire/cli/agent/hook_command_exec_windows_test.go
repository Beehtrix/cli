package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// runWindowsWrapper mirrors Codex's Windows command runner: cmd.exe /C followed
// by the raw, quoted hook command. SysProcAttr.CmdLine is required because
// cmd.exe does not use the standard Windows argv unquoting rules.
func runWindowsWrapper(t *testing.T, wrapper string, entirePresent bool) (string, string, int) {
	t.Helper()
	return runWrapperCmdLine(t, func(cmdPath string) string {
		return `"` + cmdPath + `" /C "` + wrapper + `"`
	}, entirePresent)
}

// runDroidWrapper mirrors Factory Droid's Windows command runner, which differs
// from Codex's in the two ways that decide whether a wrapper survives: `/d /s
// /c` rather than `/C`, and the hook command appended VERBATIM instead of
// wrapped in quotes (node's windowsVerbatimArguments). The stored wrapper is
// itself a `cmd.exe /d /s /c "…"` string, so this nests one cmd.exe inside
// another and only /s's strip-first-and-last-quote rule makes it work — the
// property that was verified by hand on a Windows box and is pinned here so it
// is checked before merge instead.
//
// The exe path is quoted where droid leaves it bare; with no space in
// %SystemRoot% the two command lines are equivalent, and quoting keeps the
// helper honest on a host where there is one.
func runDroidWrapper(t *testing.T, wrapper string, entirePresent bool) (string, string, int) {
	t.Helper()
	return runWrapperCmdLine(t, func(cmdPath string) string {
		return `"` + cmdPath + `" /d /s /c ` + wrapper
	}, entirePresent)
}

func runWrapperCmdLine(t *testing.T, buildCmdLine func(cmdPath string) string, entirePresent bool) (string, string, int) {
	t.Helper()
	stub := ""
	if entirePresent {
		stub = "@exit /b 0\r\n"
	}
	return runWrapperProcess(t, stub, "", func() *exec.Cmd {
		cmdPath, err := exec.LookPath("cmd.exe")
		if err != nil {
			t.Fatalf("find cmd.exe: %v", err)
		}
		cmd := exec.CommandContext(t.Context(), cmdPath)
		cmd.SysProcAttr = &syscall.SysProcAttr{
			CmdLine: buildCmdLine(cmdPath),
		}
		return cmd
	})
}

// runWrapperProcess runs the command newCmd builds with a PATH holding only
// System32 and, when entireStub is not empty, an entire.bat with that content,
// feeding it stdin, in an empty working directory. newCmd is called after PATH
// is narrowed, so anything it looks up there resolves against that PATH.
func runWrapperProcess(t *testing.T, entireStub, stdin string, newCmd func() *exec.Cmd) (string, string, int) {
	t.Helper()
	return runWrapperProcessIn(t, t.TempDir(), entireStub, stdin, newCmd)
}

// runWrapperProcessIn is runWrapperProcess with dir as the working directory.
func runWrapperProcessIn(t *testing.T, dir, entireStub, stdin string, newCmd func() *exec.Cmd) (string, string, int) {
	t.Helper()

	sysRoot := os.Getenv("SystemRoot")
	if sysRoot == "" {
		sysRoot = `C:\Windows`
	}
	// System32 supplies cmd.exe and where.exe; nothing else is on PATH so an
	// `entire` installed on the host machine can't leak into the "absent" case.
	pathEntries := []string{filepath.Join(sysRoot, "System32")}
	if entireStub != "" {
		stubDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(stubDir, "entire.bat"), []byte(entireStub), 0o700); err != nil {
			t.Fatalf("write entire stub: %v", err)
		}
		pathEntries = append([]string{stubDir}, pathEntries...)
	}
	t.Setenv("PATH", strings.Join(pathEntries, ";"))

	cmd := newCmd()
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return stdout.String(), stderr.String(), exitErr.ExitCode()
		}
		t.Fatalf("run wrapper: %v", err)
	}
	return stdout.String(), stderr.String(), 0
}

// TestWindowsWrappers_Execution verifies the cmd.exe wrappers behave correctly
// when actually executed — the gap the trail's medium finding flagged (prior
// tests asserted only string contents). It confirms the wrapped command runs
// (and propagates its exit code) when entire is present, and is skipped with a
// 0 exit when entire is absent, for both the silent and JSON-warning forms.
func TestWindowsWrappers_Execution(t *testing.T) {
	// No t.Parallel(): t.Setenv("PATH") forbids it.

	const marker = "ENTIRE_HOOK_RAN"

	t.Run("silent/present runs the command", func(t *testing.T) {
		out, stderr, code := runWindowsWrapper(t, WrapWindowsProductionSilentHookCommand("echo "+marker), true)
		if !strings.Contains(out, marker) {
			t.Fatalf("expected wrapped command to run; stdout=%q stderr=%q", out, stderr)
		}
		if code != 0 {
			t.Fatalf("expected exit 0, got %d; stderr=%q", code, stderr)
		}
	})

	t.Run("silent/present propagates the command exit code", func(t *testing.T) {
		_, stderr, code := runWindowsWrapper(t, WrapWindowsProductionSilentHookCommand("cmd /c exit 7"), true)
		if code != 7 {
			t.Fatalf("expected wrapped command exit code 7 to propagate, got %d; stderr=%q", code, stderr)
		}
	})

	t.Run("silent/absent skips the command and exits 0", func(t *testing.T) {
		out, stderr, code := runWindowsWrapper(t, WrapWindowsProductionSilentHookCommand("echo "+marker), false)
		if strings.Contains(out, marker) {
			t.Fatalf("wrapped command must NOT run when entire absent; stdout=%q stderr=%q", out, stderr)
		}
		if code != 0 {
			t.Fatalf("expected exit 0 when entire absent, got %d; stderr=%q", code, stderr)
		}
	})

	t.Run("json/absent emits valid JSON and skips the command", func(t *testing.T) {
		out, stderr, code := runWindowsWrapper(t, WrapWindowsProductionJSONWarningHookCommand("echo "+marker, WarningFormatSingleLine), false)
		if strings.Contains(out, marker) {
			t.Fatalf("wrapped command must NOT run when entire absent; stdout=%q stderr=%q", out, stderr)
		}
		if code != 0 {
			t.Fatalf("expected exit 0, got %d; stderr=%q", code, stderr)
		}
		var payload struct {
			SystemMessage string `json:"systemMessage"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &payload); err != nil {
			t.Fatalf("expected valid JSON on stdout, got %q stderr=%q (err %v)", out, stderr, err)
		}
		if !strings.Contains(payload.SystemMessage, "Entire CLI") {
			t.Fatalf("unexpected systemMessage: %q", payload.SystemMessage)
		}
	})

	t.Run("json/present runs the command without a warning", func(t *testing.T) {
		out, stderr, code := runWindowsWrapper(t, WrapWindowsProductionJSONWarningHookCommand("echo "+marker, WarningFormatSingleLine), true)
		if !strings.Contains(out, marker) {
			t.Fatalf("expected wrapped command to run; stdout=%q stderr=%q", out, stderr)
		}
		if strings.Contains(out, "systemMessage") {
			t.Fatalf("warning must NOT be emitted when entire present; stdout=%q stderr=%q", out, stderr)
		}
		if code != 0 {
			t.Fatalf("expected exit 0, got %d; stderr=%q", code, stderr)
		}
	})

	t.Run("json/present propagates the command exit code", func(t *testing.T) {
		out, stderr, code := runWindowsWrapper(
			t,
			WrapWindowsProductionJSONWarningHookCommand("cmd /c exit 7", WarningFormatSingleLine),
			true,
		)
		if strings.Contains(out, "systemMessage") {
			t.Fatalf("warning must NOT be emitted when entire present; stdout=%q stderr=%q", out, stderr)
		}
		if code != 7 {
			t.Fatalf("expected wrapped command exit code 7 to propagate, got %d; stderr=%q", code, stderr)
		}
	})
}

// TestWindowsWrappers_DroidComposition runs the wrappers droid installs through
// droid's own command runner, not Codex's. Droid nests the stored
// `cmd.exe /d /s /c "…"` inside a second cmd.exe and adds no quoting of its
// own, so a wrapper that behaves under runWindowsWrapper can still be cut apart
// here — which is the failure this whole change exists to fix, one level up.
func TestWindowsWrappers_DroidComposition(t *testing.T) {
	// No t.Parallel(): t.Setenv("PATH") forbids it.

	const marker = "ENTIRE_HOOK_RAN"

	t.Run("silent/present runs the command", func(t *testing.T) {
		out, stderr, code := runDroidWrapper(t, WrapWindowsProductionSilentHookCommand("echo "+marker), true)
		if !strings.Contains(out, marker) {
			t.Fatalf("expected wrapped command to run; stdout=%q stderr=%q", out, stderr)
		}
		if code != 0 {
			t.Fatalf("expected exit 0, got %d; stderr=%q", code, stderr)
		}
	})

	t.Run("silent/present propagates the command exit code", func(t *testing.T) {
		_, stderr, code := runDroidWrapper(t, WrapWindowsProductionSilentHookCommand("cmd /c exit 7"), true)
		if code != 7 {
			t.Fatalf("expected wrapped command exit code 7 to propagate, got %d; stderr=%q", code, stderr)
		}
	})

	t.Run("silent/absent skips the command and exits 0", func(t *testing.T) {
		out, stderr, code := runDroidWrapper(t, WrapWindowsProductionSilentHookCommand("echo "+marker), false)
		if strings.Contains(out, marker) {
			t.Fatalf("wrapped command must NOT run when entire absent; stdout=%q stderr=%q", out, stderr)
		}
		if code != 0 {
			t.Fatalf("expected exit 0 when entire absent, got %d; stderr=%q", code, stderr)
		}
	})

	// droid's Stop hook is the plain-text warning form, which is bare rather
	// than nested — the one wrapper shape whose behaviour under this runner
	// differs from the silent one.
	t.Run("plaintext/present runs the command without a warning", func(t *testing.T) {
		out, stderr, code := runDroidWrapper(t, WrapWindowsProductionPlainTextWarningHookCommand("echo "+marker, WarningFormatSingleLine), true)
		if !strings.Contains(out, marker) {
			t.Fatalf("expected wrapped command to run; stdout=%q stderr=%q", out, stderr)
		}
		if strings.Contains(out, "Entire CLI") {
			t.Fatalf("warning must NOT be emitted when entire present; stdout=%q stderr=%q", out, stderr)
		}
		if code != 0 {
			t.Fatalf("expected exit 0, got %d; stderr=%q", code, stderr)
		}
	})

	t.Run("plaintext/absent warns and skips the command", func(t *testing.T) {
		out, stderr, code := runDroidWrapper(t, WrapWindowsProductionPlainTextWarningHookCommand("echo "+marker, WarningFormatSingleLine), false)
		if strings.Contains(out, marker) {
			t.Fatalf("wrapped command must NOT run when entire absent; stdout=%q stderr=%q", out, stderr)
		}
		if !strings.Contains(out, "Entire CLI") {
			t.Fatalf("expected the warning on stdout; stdout=%q stderr=%q", out, stderr)
		}
		if code != 0 {
			t.Fatalf("expected exit 0, got %d; stderr=%q", code, stderr)
		}
	})
}

// codexWindowsShell is one way Codex on Windows runs a hook's commandWindows
// (codex-rs/hooks/src/engine/command_runner.rs, build_command).
type codexWindowsShell struct {
	name string
	// run runs hook in dir, with an entire.bat holding entireStub on PATH
	// unless it is empty.
	run func(t *testing.T, dir, hook, entireStub, stdin string) (string, string, int)
}

// codexWindowsShells lists the runners Codex uses on Windows that this host
// can provide: PowerShell, which Codex runs hooks through whenever the session
// has a shell (its default Windows shell is PowerShell, pwsh first), and
// %COMSPEC% /C with the hook quoted raw, its fallback.
func codexWindowsShells(t *testing.T) []codexWindowsShell {
	t.Helper()
	shells := []codexWindowsShell{{
		name: "cmd /C",
		run: func(t *testing.T, dir, hook, entireStub, stdin string) (string, string, int) {
			t.Helper()
			return runWrapperProcessIn(t, dir, entireStub, stdin, func() *exec.Cmd {
				cmdPath, err := exec.LookPath("cmd.exe")
				if err != nil {
					t.Fatalf("find cmd.exe: %v", err)
				}
				cmd := exec.CommandContext(t.Context(), cmdPath)
				cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `"` + cmdPath + `" /C "` + hook + `"`}
				return cmd
			})
		},
	}}
	// Resolved before runWrapperProcess narrows PATH to System32, which holds
	// neither. The hook is one ordinary argument after -Command: Codex passes it
	// with Rust's std quoting, which follows the same rules as Go's.
	for _, exe := range []string{"powershell.exe", "pwsh.exe"} {
		path, err := exec.LookPath(exe)
		if err != nil {
			if exe == "powershell.exe" {
				t.Fatalf("find %s: %v", exe, err)
			}
			t.Logf("%s not installed; not covered on this host", exe)
			continue
		}
		shells = append(shells, codexWindowsShell{
			name: exe,
			run: func(t *testing.T, dir, hook, entireStub, stdin string) (string, string, int) {
				t.Helper()
				return runWrapperProcessIn(t, dir, entireStub, stdin, func() *exec.Cmd {
					return exec.CommandContext(t.Context(), path, "-NoProfile", "-Command", hook)
				})
			},
		})
	}
	return shells
}

// TestWindowsNestedWrappers_CodexShells runs the wrappers Entire writes into
// Codex's commandWindows through every shell Codex may hand them to on
// Windows. The direct cmd.exe forms do not survive PowerShell, which reads `&`
// and `if errorlevel` as its own syntax, and the sh wrapper needs an sh on PATH
// and Codex to pass its quotes through intact (#2884). The stub reads the hook
// payload from stdin, so this also checks the payload reaches entire through
// both layers of shell.
func TestWindowsNestedWrappers_CodexShells(t *testing.T) {
	// No t.Parallel(): t.Setenv("PATH") forbids it.

	const (
		ranMarker    = "ENTIRE_HOOK_RAN"
		hijackMarker = "CHECKOUT_ENTIRE_RAN"
		payload      = `{"session_id":"s-2884","hook_event_name":"Stop"}`
	)
	stub := "@echo off\r\necho " + ranMarker + " %*\r\nfindstr /c:s-2884\r\nexit /b 0\r\n"
	// A version-manager shim with no version selected: on PATH, but fails.
	brokenStub := "@echo off\r\nif \"%1\"==\"--version\" exit /b 3\r\necho " + ranMarker + " %*\r\nexit /b 0\r\n"
	missing := windowsPlainText(MissingEntireWarning(WarningFormatSingleLine))
	failed := windowsPlainText(FailedEntireWarning(WarningFormatSingleLine))
	silent := WrapWindowsProductionNestedSilentHookCommand("entire hooks codex stop")
	warn := WrapWindowsProductionNestedPlainTextWarningHookCommand("entire hooks codex session-start", WarningFormatSingleLine)
	// checkout is a working directory holding an entire.cmd and entire.bat of
	// its own, the way a branch under review could.
	checkout := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		for _, name := range []string{"entire.cmd", "entire.bat"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("@echo "+hijackMarker+"\r\n"), 0o700); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
		return dir
	}

	for _, shell := range codexWindowsShells(t) {
		t.Run(shell.name+"/silent/present runs entire with the payload", func(t *testing.T) {
			out, stderr, code := shell.run(t, t.TempDir(), silent, stub, payload)
			if !strings.Contains(out, ranMarker+" hooks codex stop") {
				t.Fatalf("expected entire to run; stdout=%q stderr=%q", out, stderr)
			}
			if !strings.Contains(out, payload) {
				t.Fatalf("expected the hook payload to reach entire on stdin; stdout=%q stderr=%q", out, stderr)
			}
			if code != 0 {
				t.Fatalf("expected exit 0, got %d; stderr=%q", code, stderr)
			}
		})

		t.Run(shell.name+"/silent/absent exits 0 without output", func(t *testing.T) {
			out, stderr, code := shell.run(t, t.TempDir(), silent, "", payload)
			if strings.TrimSpace(out) != "" {
				t.Fatalf("expected no stdout when entire is absent; stdout=%q stderr=%q", out, stderr)
			}
			if code != 0 {
				t.Fatalf("expected exit 0 when entire absent, got %d; stderr=%q", code, stderr)
			}
		})

		t.Run(shell.name+"/silent/runs the entire on PATH, not the checkout's", func(t *testing.T) {
			out, stderr, _ := shell.run(t, checkout(t), silent, stub, payload)
			if strings.Contains(out, hijackMarker) || !strings.Contains(out, ranMarker+" hooks codex stop") {
				t.Fatalf("expected the entire on PATH to run; stdout=%q stderr=%q", out, stderr)
			}
		})

		t.Run(shell.name+"/silent/never runs the checkout's entire", func(t *testing.T) {
			// where.exe may still list the checkout's entire.cmd, in which case
			// cmd.exe, looking on PATH only, reports entire missing and the hook
			// fails; either way the checkout's file must not run.
			out, stderr, _ := shell.run(t, checkout(t), silent, "", payload)
			if strings.Contains(out, hijackMarker) {
				t.Fatalf("the checkout's entire ran; stdout=%q stderr=%q", out, stderr)
			}
		})

		t.Run(shell.name+"/warning/present runs entire without a warning", func(t *testing.T) {
			out, stderr, code := shell.run(t, t.TempDir(), warn, stub, payload)
			if !strings.Contains(out, ranMarker+" hooks codex session-start") {
				t.Fatalf("expected entire to run; stdout=%q stderr=%q", out, stderr)
			}
			if !strings.Contains(out, payload) {
				t.Fatalf("expected the hook payload to reach entire on stdin; stdout=%q stderr=%q", out, stderr)
			}
			if strings.Contains(out, "Entire CLI") {
				t.Fatalf("warning must NOT be emitted when entire present; stdout=%q stderr=%q", out, stderr)
			}
			if code != 0 {
				t.Fatalf("expected exit 0, got %d; stderr=%q", code, stderr)
			}
		})

		t.Run(shell.name+"/warning/absent prints the warning and exits 0", func(t *testing.T) {
			out, stderr, code := shell.run(t, t.TempDir(), warn, "", payload)
			if strings.TrimSpace(out) != missing {
				t.Fatalf("expected exactly the warning on stdout; got %q, want %q; stderr=%q", strings.TrimSpace(out), missing, stderr)
			}
			if code != 0 {
				t.Fatalf("expected exit 0, got %d; stderr=%q", code, stderr)
			}
		})

		t.Run(shell.name+"/warning/failing entire prints the failed warning and exits 0", func(t *testing.T) {
			out, stderr, code := shell.run(t, t.TempDir(), warn, brokenStub, payload)
			if strings.TrimSpace(out) != failed {
				t.Fatalf("expected exactly the failed warning on stdout; got %q, want %q; stderr=%q", strings.TrimSpace(out), failed, stderr)
			}
			if code != 0 {
				t.Fatalf("expected exit 0, got %d; stderr=%q", code, stderr)
			}
		})

		t.Run(shell.name+"/warning/never runs the checkout's entire", func(t *testing.T) {
			out, stderr, code := shell.run(t, checkout(t), warn, "", payload)
			if strings.Contains(out, hijackMarker) {
				t.Fatalf("the checkout's entire ran; stdout=%q stderr=%q", out, stderr)
			}
			if got := strings.TrimSpace(out); got != missing && got != failed {
				t.Fatalf("expected a warning; got %q; stderr=%q", got, stderr)
			}
			if code != 0 {
				t.Fatalf("expected exit 0, got %d; stderr=%q", code, stderr)
			}
		})

		t.Run(shell.name+"/silent/present passes a failure through", func(t *testing.T) {
			_, stderr, code := shell.run(t, t.TempDir(), WrapWindowsProductionNestedSilentHookCommand("cmd /c exit 7"), stub, payload)
			// cmd.exe hands the exit code back unchanged; Windows PowerShell 5.1
			// reports any failing last command as 1. Codex only tells zero from
			// non-zero for these events.
			if code == 0 {
				t.Fatalf("expected a non-zero exit to pass through, got 0; stderr=%q", stderr)
			}
		})
	}

	// The control: without the PATH-only lookup, cmd.exe runs the checkout's
	// entire, which is what the cases above rule out. If this stops finding
	// it, the checkout fixture no longer tests anything.
	t.Run("control/plain cmd.exe lookup runs the checkout's entire", func(t *testing.T) {
		plain := `cmd.exe /d /s /c "where.exe entire >nul 2>nul & if errorlevel 1 (ver>nul) else (entire hooks codex stop)"`
		out, stderr, _ := codexWindowsShells(t)[0].run(t, checkout(t), plain, "", payload)
		if !strings.Contains(out, hijackMarker) {
			t.Fatalf("expected the checkout's entire to run without the PATH-only lookup; stdout=%q stderr=%q", out, stderr)
		}
	})
}
