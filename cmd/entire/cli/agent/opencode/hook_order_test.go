package opencode

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/proclive"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

// hookOrderRepo makes a repository the current directory and returns the
// directory hook markers live in.
func hookOrderRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testutil.InitRepo(t, dir)
	t.Chdir(dir)
	paths.ClearWorktreeRootCache()
	t.Cleanup(paths.ClearWorktreeRootCache)
	return filepath.Join(dir, ".entire", "tmp", "opencode-hooks")
}

// startSleeper runs a short-lived process and returns its identity.
func startSleeper(t *testing.T, d time.Duration) proclive.Identity {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("process liveness is not introspectable on Windows")
	}
	cmd := exec.CommandContext(t.Context(), "sleep", strconv.FormatFloat(d.Seconds(), 'f', 3, 64))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Reap it as soon as it exits, as Node does for the hooks it spawns: an
	// unreaped zombie still looks alive.
	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait() //nolint:errcheck // exit status irrelevant
		close(exited)
	}()
	t.Cleanup(func() { <-exited })
	id, ok := proclive.IdentityOf(cmd.Process.Pid)
	if !ok {
		t.Skip("process identity unavailable on this platform")
	}
	return id
}

func writeTestMarker(t *testing.T, dir, name string, marker hookMarker) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// OpenCode 1 payloads carry no ordering fields: nothing is waited on or
// written.
func TestOrderHook_IgnoresOpenCode1Payloads(t *testing.T) {
	// Not parallel: t.Chdir.
	dir := hookOrderRepo(t)
	orderHook(t.Context(), []byte(`{"session_id":"ses_1"}`))()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("hook order dir created for an OpenCode 1 payload: %v", err)
	}
}

// A detached hook marks itself running for the length of the hook.
func TestOrderHook_DetachedHookMarksItselfRunning(t *testing.T) {
	// Not parallel: t.Chdir.
	dir := hookOrderRepo(t)
	release := orderHook(t.Context(), []byte(`{"session_id":"ses_1","after_pids":[]}`))
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "ses_1.") {
		t.Fatalf("markers = %v (err %v), want one for ses_1", entries, err)
	}
	release()
	if entries, _ := os.ReadDir(dir); len(entries) != 0 { //nolint:errcheck // empty on error is fine here
		t.Fatalf("marker left after the hook finished: %v", entries)
	}
}

// A detached hook waits for the hooks it follows to exit.
func TestOrderHook_DetachedHookWaitsForPredecessors(t *testing.T) {
	// Not parallel: t.Chdir.
	hookOrderRepo(t)
	pred := startSleeper(t, 400*time.Millisecond)
	start := time.Now()
	orderHook(t.Context(), []byte(`{"session_id":"ses_1","after_pids":[`+strconv.Itoa(pred.PID)+`]}`))()
	if elapsed := time.Since(start); elapsed < 300*time.Millisecond {
		t.Fatalf("returned after %s; want it to wait for the predecessor", elapsed)
	}
}

// A new run's session-start waits for the previous run's running hooks of
// the session, ignores other sessions' hooks, and clears markers of hooks
// that are gone.
func TestOrderHook_SessionStartWaitsForRunningHooks(t *testing.T) {
	// Not parallel: t.Chdir.
	dir := hookOrderRepo(t)
	running := startSleeper(t, 400*time.Millisecond)
	now := time.Now().UnixMilli()
	writeTestMarker(t, dir, "ses_1.1.a.json", hookMarker{Process: running, CreatedAt: now})
	other := writeTestMarker(t, dir, "ses_2.1.a.json", hookMarker{Process: running, CreatedAt: now})
	dead := writeTestMarker(t, dir, "ses_1.2.b.json", hookMarker{Process: proclive.Identity{PID: running.PID, Start: "not-this-process", Host: running.Host, Boot: running.Boot}, CreatedAt: now})

	start := time.Now()
	orderHook(t.Context(), []byte(`{"session_id":"ses_1","wait_detached":true}`))()
	if elapsed := time.Since(start); elapsed < 300*time.Millisecond || elapsed > 5*time.Second {
		t.Fatalf("returned after %s; want it to wait for the running hook only", elapsed)
	}
	if _, err := os.Stat(dead); !os.IsNotExist(err) {
		t.Error("marker of a gone process was not cleared")
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("another session's marker was removed while its hook runs")
	}
}

// A marker whose process cannot be checked holds a new run only while it is
// young.
func TestMarkerState_UnknownLivenessAgesOut(t *testing.T) {
	// Not parallel: t.Chdir.
	dir := hookOrderRepo(t)
	root, err := hookOrderRoot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	unknown := proclive.Identity{} // no identity: liveness unknown
	writeTestMarker(t, dir, "ses_1.young.json", hookMarker{Process: unknown, CreatedAt: time.Now().UnixMilli()})
	writeTestMarker(t, dir, "ses_1.old.json", hookMarker{Process: unknown, CreatedAt: time.Now().Add(-unknownMarkerMaxAge - time.Minute).UnixMilli()})
	if got := markerState(root, hookOrderDirName+"/ses_1.young.json"); got != markerLive {
		t.Errorf("young unknown marker = %v, want live", got)
	}
	if got := markerState(root, hookOrderDirName+"/ses_1.old.json"); got != markerStale {
		t.Errorf("old unknown marker = %v, want stale", got)
	}
}
