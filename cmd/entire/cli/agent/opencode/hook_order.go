package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/entiredir"
	"github.com/entireio/cli/cmd/entire/cli/jsonutil"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/osroot"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/proclive"
	"github.com/entireio/cli/cmd/entire/cli/validation"
)

// OpenCode 2 runs turn-end, session-end, compaction and held subagent stops
// detached (`opencode run --standalone` kills plugin cleanup about 3s after
// a turn), so a session's hooks can still be running when the next
// `opencode run` starts. The OpenCode 2 plugin marks those payloads:
//
//   - after_pids: the session's detached hooks this one must follow (within
//     one plugin process). The hook waits for them to exit before it runs,
//     and while it runs it leaves a marker under .entire/tmp/opencode-hooks.
//   - wait_detached: set on session-start, the first hook of every plugin
//     instance. It waits until no marker of the session belongs to a live
//     process, so a new run never overtakes the previous run's hooks.
//
// OpenCode 1 payloads carry neither and are unaffected. Every step fails
// open: ordering is best effort and must never lose or block a hook for long.

// hookOrderRaw holds the ordering fields of any OpenCode hook payload.
type hookOrderRaw struct {
	SessionID    string `json:"session_id"`
	AfterPIDs    *[]int `json:"after_pids"`
	WaitDetached bool   `json:"wait_detached"`
}

const (
	// detachedWaitTimeout bounds a detached hook's wait for its predecessors.
	// Nothing waits on a detached hook, so it can be generous; it only stops
	// a stuck predecessor from holding the session forever.
	detachedWaitTimeout = 60 * time.Second
	// sessionStartWaitTimeout bounds the new run's wait, which delays the
	// user's first prompt; it stays well under the plugin's hook timeout.
	sessionStartWaitTimeout = 20 * time.Second
	// unknownMarkerMaxAge ends the wait on a marker whose process cannot be
	// checked (no process introspection, as on Windows) and whose hook did
	// not remove it.
	unknownMarkerMaxAge = 2 * time.Minute
	hookOrderPoll       = 100 * time.Millisecond
)

var hookOrderDirName = entireTmpName + "/opencode-hooks"

// hookMarker is the content of a running detached hook's marker.
type hookMarker struct {
	Process   proclive.Identity `json:"process"`
	CreatedAt int64             `json:"created_at"` // Unix ms
}

// orderHook applies the payload's ordering fields before the hook runs and
// returns a function that ends its marker once the hook is done.
func orderHook(ctx context.Context, payload []byte) func() {
	var order hookOrderRaw
	if err := json.Unmarshal(payload, &order); err != nil || (order.AfterPIDs == nil && !order.WaitDetached) {
		return func() {}
	}
	if validation.ValidateSessionID(order.SessionID) != nil {
		return func() {}
	}
	ctx = logging.WithComponent(ctx, "opencode")
	root, err := hookOrderRoot(ctx)
	if err != nil {
		logging.Debug(ctx, "opencode hook ordering unavailable", slog.String("error", err.Error()))
		return func() {}
	}
	if order.WaitDetached {
		waitForMarkers(ctx, root, order.SessionID)
		return func() {}
	}
	release := writeMarker(ctx, root, order.SessionID)
	waitForPIDs(ctx, *order.AfterPIDs)
	return release
}

func hookOrderRoot(ctx context.Context) (*os.Root, error) {
	repoRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve worktree: %w", err)
	}
	root, err := entiredir.OpenAt(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", paths.EntireDir, err)
	}
	if err := osroot.MkdirAllNoSymlink(root, hookOrderDirName, 0o750); err != nil {
		return nil, fmt.Errorf("create hook order dir: %w", err)
	}
	return root, nil
}

// writeMarker records this process as a running detached hook of the
// session and returns the function that removes the marker.
func writeMarker(ctx context.Context, root *os.Root, sessionID string) func() {
	self, ok := proclive.IdentityOf(os.Getpid())
	if !ok {
		self = proclive.Identity{PID: os.Getpid()}
	}
	data, err := json.Marshal(hookMarker{Process: self, CreatedAt: time.Now().UnixMilli()})
	if err != nil {
		return func() {}
	}
	name := hookOrderDirName + "/" + sessionID + "." + strconv.Itoa(os.Getpid()) + "." + strconv.FormatInt(time.Now().UnixNano(), 36) + ".json"
	if err := jsonutil.WriteFileAtomicIn(root, name, data, 0o600); err != nil {
		logging.Debug(ctx, "opencode hook marker not written", slog.String("error", err.Error()))
		return func() {}
	}
	return func() { _ = root.Remove(name) } //nolint:errcheck // best effort; a stale marker ends with its process
}

// waitForPIDs waits until each process has exited, bounded.
func waitForPIDs(ctx context.Context, pids []int) {
	var waiting []proclive.Identity
	for _, pid := range pids {
		if pid <= 0 || pid == os.Getpid() {
			continue
		}
		if id, ok := proclive.IdentityOf(pid); ok {
			waiting = append(waiting, id)
		}
	}
	waitUntil(ctx, detachedWaitTimeout, func() bool {
		for _, id := range waiting {
			if proclive.Check(id) == proclive.LivenessAlive {
				return false
			}
		}
		return true
	})
}

// waitForMarkers waits until no marker of the session belongs to a running
// hook, bounded, removing markers of processes that are gone.
func waitForMarkers(ctx context.Context, root *os.Root, sessionID string) {
	waitUntil(ctx, sessionStartWaitTimeout, func() bool {
		return !liveMarkerExists(root, sessionID)
	})
}

func liveMarkerExists(root *os.Root, sessionID string) bool {
	dir, err := root.Open(hookOrderDirName)
	if err != nil {
		return false
	}
	defer dir.Close()
	entries, err := dir.ReadDir(-1)
	if err != nil {
		return false
	}
	live := false
	for _, entry := range entries {
		if !entry.Type().IsRegular() || !strings.HasPrefix(entry.Name(), sessionID+".") {
			continue
		}
		name := hookOrderDirName + "/" + entry.Name()
		switch markerState(root, name) {
		case markerLive:
			live = true
		case markerStale:
			_ = root.Remove(name) //nolint:errcheck // best effort cleanup
		case markerUnreadable:
		}
	}
	return live
}

type markerStatus int

const (
	markerLive markerStatus = iota
	markerStale
	// markerUnreadable is neither waited on nor removed: it may be a marker
	// still being written, or one removed since the directory was listed.
	markerUnreadable
)

func markerState(root *os.Root, name string) markerStatus {
	data, err := entiredir.ReadFile(root, name)
	if err != nil {
		return markerUnreadable
	}
	var marker hookMarker
	if json.Unmarshal(data, &marker) != nil {
		return markerUnreadable
	}
	switch proclive.Check(marker.Process) {
	case proclive.LivenessAlive:
		return markerLive
	case proclive.LivenessDead:
		return markerStale
	case proclive.LivenessUnknown:
		if time.Since(time.UnixMilli(marker.CreatedAt)) < unknownMarkerMaxAge {
			return markerLive
		}
		return markerStale
	default:
		return markerStale
	}
}

// waitUntil polls done until it reports true, the timeout passes or ctx ends.
func waitUntil(ctx context.Context, timeout time.Duration, done func() bool) {
	deadline := time.Now().Add(timeout)
	for !done() {
		if time.Now().After(deadline) {
			logging.Debug(ctx, "opencode hook ordering wait timed out", slog.Duration("timeout", timeout))
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(hookOrderPoll):
		}
	}
}
