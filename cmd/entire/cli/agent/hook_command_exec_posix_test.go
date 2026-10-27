//go:build !windows

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// entireStandIn picks what `entire` resolves to on the wrapper's PATH.
type entireStandIn int

const (
	entireMissing entireStandIn = iota
	// entireBroken is on PATH but exits non-zero, like a version-manager shim
	// with no version selected.
	entireBroken
	// entireWorking echoes the hook payload from stdin, which also proves the
	// wrapper hands its stdin through.
	entireWorking
)

// runPOSIXWrapper runs a stored hook command the way agents do: the whole
// string handed to sh, with the hook payload on stdin. PATH holds only the
// stand-ins, so an `entire` installed on the host can't leak in.
func runPOSIXWrapper(t *testing.T, wrapper string, entire entireStandIn, stdin string) (string, int) {
	t.Helper()

	binDir := t.TempDir()
	workDir := t.TempDir()
	linkSystemBinary(t, binDir, "sh", "sh")
	switch entire {
	case entireBroken:
		linkSystemBinary(t, binDir, "entire", "false")
	case entireWorking:
		// `entire hooks …` becomes `sh hooks …`, which reads the script named
		// hooks from the working directory. sh reads it rather than exec-ing it,
		// so writing it cannot race into ETXTBSY.
		linkSystemBinary(t, binDir, "entire", "sh")
		linkSystemBinary(t, binDir, "cat", "cat")
		if err := os.WriteFile(filepath.Join(workDir, "hooks"), []byte("cat\n"), 0o600); err != nil {
			t.Fatalf("write hooks script: %v", err)
		}
	case entireMissing:
	}

	cmd := exec.CommandContext(context.Background(), filepath.Join(binDir, "sh"), "-c", wrapper)
	cmd.Dir = workDir
	cmd.Env = []string{"PATH=" + binDir}
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0
	case errors.As(err, &exitErr):
		return string(out), exitErr.ExitCode()
	default:
		t.Fatalf("run wrapper: %v", err)
		return "", 0
	}
}

// linkSystemBinary symlinks rather than writes stand-ins: a freshly written
// executable can fail with ETXTBSY when a parallel test forks while it is open.
func linkSystemBinary(t *testing.T, dir, name, target string) {
	t.Helper()

	realPath, err := exec.LookPath(target)
	if err != nil {
		t.Skipf("%s not available on PATH", target)
	}
	if err := os.Symlink(realPath, filepath.Join(dir, name)); err != nil {
		t.Fatalf("link %s: %v", name, err)
	}
}

func TestWarningWrappers_Execution(t *testing.T) {
	t.Parallel()

	const payload = `{"session_id":"s1"}`
	command := "entire hooks test-agent session-start"

	jsonMessage := func(t *testing.T, stdout string) string {
		t.Helper()
		var got struct {
			SystemMessage string `json:"systemMessage"`
		}
		if err := json.Unmarshal([]byte(stdout), &got); err != nil {
			t.Fatalf("stdout is not a JSON hook response: %v\n%q", err, stdout)
		}
		return got.SystemMessage
	}
	plainMessage := func(_ *testing.T, stdout string) string {
		return strings.TrimSuffix(stdout, "\n")
	}

	wrappers := []struct {
		name    string
		wrapper string
		format  WarningFormat
		message func(*testing.T, string) string
	}{
		{"json", WrapProductionJSONWarningHookCommand(command, WarningFormatMultiLine), WarningFormatMultiLine, jsonMessage},
		{"plain text", WrapProductionPlainTextWarningHookCommand(command, WarningFormatSingleLine), WarningFormatSingleLine, plainMessage},
	}

	for _, w := range wrappers {
		t.Run(w.name+"/entire missing warns and exits 0", func(t *testing.T) {
			t.Parallel()
			stdout, code := runPOSIXWrapper(t, w.wrapper, entireMissing, payload)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0", code)
			}
			if got, want := w.message(t, stdout), MissingEntireWarning(w.format); got != want {
				t.Fatalf("warning = %q, want %q", got, want)
			}
		})

		t.Run(w.name+"/entire that cannot run warns and exits 0", func(t *testing.T) {
			t.Parallel()
			stdout, code := runPOSIXWrapper(t, w.wrapper, entireBroken, payload)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0", code)
			}
			if got, want := w.message(t, stdout), FailedEntireWarning(w.format); got != want {
				t.Fatalf("warning = %q, want %q", got, want)
			}
		})

		t.Run(w.name+"/working entire passes stdin and output through", func(t *testing.T) {
			t.Parallel()
			stdout, code := runPOSIXWrapper(t, w.wrapper, entireWorking, payload)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0", code)
			}
			if stdout != payload {
				t.Fatalf("stdout = %q, want only entire's output %q", stdout, payload)
			}
		})
	}
}
