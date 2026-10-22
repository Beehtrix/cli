package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/jsonutil"
	"github.com/entireio/cli/cmd/entire/cli/strategy"
	"github.com/entireio/cli/cmd/entire/cli/stringutil"
	"github.com/entireio/cli/cmd/entire/cli/tuiutil"
)

// outgoingCheckpointsJSON is `entire checkpoint list --outgoing --json`. The
// checkpoint_sync_* and checkpoint_push_disabled fields carry the same values
// as in `entire status --json`. Checkpoints is never null; it is empty when
// Complete is false, because local state cannot tell what the push sends.
type outgoingCheckpointsJSON struct {
	Backend           string                   `json:"backend"`
	SyncRemote        string                   `json:"checkpoint_sync_remote,omitempty"`
	SyncRemoteSource  string                   `json:"checkpoint_sync_remote_source,omitempty"`
	SyncError         string                   `json:"checkpoint_sync_error,omitempty"`
	PushDisabled      bool                     `json:"checkpoint_push_disabled"`
	Complete          bool                     `json:"complete"`
	Reason            string                   `json:"reason,omitempty"`
	FirstPushDeferred bool                     `json:"first_push_deferred,omitempty"`
	StaleQueueEntries int                      `json:"stale_queue_entries,omitempty"`
	Checkpoints       []outgoingCheckpointJSON `json:"checkpoints"`
}

// outgoingCheckpointJSON is one checkpoint the next push sends. A queued ref
// that names no checkpoint, or a checkpoint that cannot be read, is still sent,
// so it is listed with Error set instead of being dropped.
type outgoingCheckpointJSON struct {
	CheckpointID string   `json:"checkpoint_id,omitempty"`
	Ref          string   `json:"ref,omitempty"`
	CreatedAt    string   `json:"created_at,omitempty"`
	Agent        string   `json:"agent,omitempty"`
	SessionCount int      `json:"session_count,omitempty"`
	Prompt       string   `json:"prompt,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	FilesTouched []string `json:"files_touched"`
	Updated      bool     `json:"updated"`
	Error        string   `json:"error,omitempty"`
}

// outgoingListing is the --outgoing dataset: where checkpoints sync to, what
// the next push sends there, and what each of those checkpoints holds.
type outgoingListing struct {
	sync    checkpointSyncInfo
	result  strategy.OutgoingCheckpoints
	reason  string
	entries []outgoingCheckpointRow
}

type outgoingCheckpointRow struct {
	strategy.OutgoingCheckpoint

	createdAt    time.Time
	agent        string
	sessionCount int
	prompt       string
	summary      string
	files        []string
	err          error
}

func loadOutgoingListing(ctx context.Context) (outgoingListing, error) {
	s, err := LoadEntireSettings(ctx)
	if err != nil {
		return outgoingListing{}, err
	}
	listing := outgoingListing{sync: computeCheckpointSyncInfo(ctx, s)}

	// The tracking ref to compare against belongs to the elected remote. A
	// dedicated checkpoint_remote is a URL with no tracking ref.
	remoteName := listing.sync.Remote
	if listing.sync.Source == checkpointSyncSourceDedicated {
		remoteName = ""
	}
	listing.result, err = strategy.ListOutgoingCheckpoints(ctx, remoteName)
	if err != nil {
		return outgoingListing{}, fmt.Errorf("list outgoing checkpoints: %w", err)
	}
	if !listing.result.Known {
		listing.reason = outgoingUnknownReason(listing.sync)
		return listing, nil
	}
	if len(listing.result.Checkpoints) == 0 {
		return listing, nil
	}

	repo, err := openRepository(ctx)
	if err != nil {
		return outgoingListing{}, fmt.Errorf("not a git repository: %w", err)
	}
	defer repo.Close()
	// No fetchers: every outgoing checkpoint was written here, and a read must
	// never reach the remote. v1 reads stay on the local branch, which is what
	// the push sends.
	refs := checkpoint.ResolveRefs(ctx).PrimaryAsLocalRead()
	stores, err := checkpoint.Open(ctx, repo, checkpoint.OpenOptions{
		Refs: &refs,
		// Deliberately local: remote-tracking tiers hold what the remote
		// already has, not what this push sends.
		ReadRemotes: nil,
	})
	if err != nil {
		return outgoingListing{}, fmt.Errorf("open checkpoint store: %w", err)
	}
	for _, oc := range listing.result.Checkpoints {
		listing.entries = append(listing.entries, readOutgoingCheckpoint(ctx, stores.Persistent, oc))
	}
	// Oldest first; rows without a creation time (unreadable) go last.
	slices.SortStableFunc(listing.entries, func(a, b outgoingCheckpointRow) int {
		switch {
		case a.createdAt.IsZero() != b.createdAt.IsZero():
			if a.createdAt.IsZero() {
				return 1
			}
			return -1
		default:
			return a.createdAt.Compare(b.createdAt)
		}
	})
	return listing, nil
}

func outgoingUnknownReason(info checkpointSyncInfo) string {
	switch {
	case info.Source == checkpointSyncSourceDedicated:
		return fmt.Sprintf("Checkpoints sync to the dedicated checkpoint remote (%s). The git-branch backend keeps no local record of what that remote holds, so this cannot list what the next push sends without asking it.", info.Remote)
	case info.Err != "":
		return "No checkpoint sync remote is elected, so nothing local records what a push would send."
	default:
		return "No git remote is configured, so 'git push' has nowhere to send checkpoints."
	}
}

// readOutgoingCheckpoint reads what the listing shows for one checkpoint. The
// prompt comes from the stored prompt.txt, which redaction already ran over.
func readOutgoingCheckpoint(ctx context.Context, store checkpoint.PersistentStore, oc strategy.OutgoingCheckpoint) outgoingCheckpointRow {
	row := outgoingCheckpointRow{OutgoingCheckpoint: oc}
	if oc.CheckpointID.IsEmpty() {
		row.err = errors.New("ref does not name a checkpoint")
		return row
	}
	summary, err := checkpoint.ReadCheckpoint(ctx, store, oc.CheckpointID)
	if err != nil {
		row.err = err
		return row
	}
	row.files = summary.FilesTouched
	row.sessionCount = len(summary.Sessions)
	if row.sessionCount == 0 {
		return row
	}
	first, err := store.ReadSessionMetadata(ctx, oc.CheckpointID, 0)
	if err != nil {
		row.err = fmt.Errorf("read session metadata: %w", err)
		return row
	}
	row.createdAt = first.CreatedAt
	latest := first
	if row.sessionCount > 1 {
		if latest, err = store.ReadSessionMetadata(ctx, oc.CheckpointID, row.sessionCount-1); err != nil {
			row.err = fmt.Errorf("read session metadata: %w", err)
			return row
		}
	}
	row.agent = string(latest.Agent)
	if latest.Summary != nil {
		row.summary = latest.Summary.Intent
	}
	row.prompt = readLatestCommittedSessionPrompt(ctx, store, oc.CheckpointID, row.sessionCount)
	return row
}

func (l outgoingListing) json() outgoingCheckpointsJSON {
	out := outgoingCheckpointsJSON{
		Backend:           checkpoint.BackendTypeGitBranch,
		SyncRemote:        l.sync.Remote,
		SyncRemoteSource:  l.sync.Source,
		SyncError:         l.sync.Err,
		PushDisabled:      l.sync.PushDisabled,
		Complete:          l.result.Known,
		Reason:            l.reason,
		FirstPushDeferred: l.result.FirstPushDeferred,
		StaleQueueEntries: l.result.StaleQueueEntries,
		Checkpoints:       make([]outgoingCheckpointJSON, 0, len(l.entries)),
	}
	if l.result.PrimaryIsRefs {
		out.Backend = checkpoint.BackendTypeGitRefs
	}
	for _, row := range l.entries {
		entry := outgoingCheckpointJSON{
			CheckpointID: row.CheckpointID.String(),
			Ref:          row.Ref.String(),
			Agent:        row.agent,
			SessionCount: row.sessionCount,
			Prompt:       row.prompt,
			Summary:      row.summary,
			FilesTouched: row.files,
			Updated:      row.Updated,
		}
		if entry.FilesTouched == nil {
			entry.FilesTouched = []string{}
		}
		if !row.createdAt.IsZero() {
			entry.CreatedAt = row.createdAt.UTC().Format(time.RFC3339)
		}
		if row.err != nil {
			entry.Error = row.err.Error()
		}
		out.Checkpoints = append(out.Checkpoints, entry)
	}
	return out
}

func runCheckpointOutgoingListJSON(ctx context.Context, w io.Writer) error {
	listing, err := loadOutgoingListing(ctx)
	if err != nil {
		return err
	}
	data, err := jsonutil.MarshalIndentWithNewline(listing.json(), "", "  ")
	if err != nil {
		return fmt.Errorf("encode outgoing checkpoints: %w", err)
	}
	fmt.Fprint(w, string(data))
	return nil
}

func runCheckpointOutgoingListHuman(ctx context.Context, w io.Writer) error {
	listing, err := loadOutgoingListing(ctx)
	if err != nil {
		return err
	}
	renderOutgoingListing(w, listing)
	return nil
}

// outgoingDestination names where checkpoints sync to, for prose.
func outgoingDestination(info checkpointSyncInfo) string {
	if info.Source == checkpointSyncSourceDedicated {
		return "the dedicated checkpoint remote (" + tuiutil.SanitizeTerminalLabel(info.Remote) + ")"
	}
	return tuiutil.SanitizeTerminalLabel(info.Remote)
}

func renderOutgoingListing(w io.Writer, l outgoingListing) {
	info := l.sync
	n := len(l.entries)
	dest := outgoingDestination(info)

	if info.Err != "" {
		label := "! Checkpoints NOT syncing: "
		if info.PushDisabled {
			label = "! Checkpoint remote configuration: "
		}
		fmt.Fprintln(w, label+tuiutil.SanitizeTerminalLabel(info.Err))
	}
	if !l.result.Known {
		// The reason can quote the checkpoint_remote repo from committed settings.
		fmt.Fprintln(w, tuiutil.SanitizeDisplayText(l.reason))
		return
	}
	if info.PushDisabled {
		if n > 0 {
			fmt.Fprintln(w, "Automatic checkpoint pushing is off (push_sessions=false), so 'git push' sends none of these.")
		} else {
			fmt.Fprintln(w, "Automatic checkpoint pushing is off (push_sessions=false).")
		}
	}
	if n == 0 {
		switch {
		case dest == "":
			fmt.Fprintln(w, "No outgoing checkpoints.")
		case info.PushDisabled:
			fmt.Fprintf(w, "%s has every local checkpoint.\n", dest)
		default:
			fmt.Fprintf(w, "No checkpoints waiting to go to %s.\n", dest)
		}
		renderStaleQueueEntries(w, l.result.StaleQueueEntries)
		return
	}

	fmt.Fprintln(w, outgoingHeadline(info, l.result, n, dest))
	fmt.Fprintln(w)
	anyUpdated := false
	for _, row := range l.entries {
		renderOutgoingRow(w, row)
		anyUpdated = anyUpdated || row.Updated
	}

	if anyUpdated {
		where := dest
		if where == "" {
			where = "the remote"
		}
		if l.result.PrimaryIsRefs {
			fmt.Fprintf(w, "updated: written more than once, so %s may already have an earlier version.\n", where)
		} else {
			fmt.Fprintf(w, "updated: %s already has an earlier version, and the push sends the changes.\n", where)
		}
	}
	renderStaleQueueEntries(w, l.result.StaleQueueEntries)
	fmt.Fprintln(w, "Details: entire checkpoint explain <id>")
}

func outgoingHeadline(info checkpointSyncInfo, result strategy.OutgoingCheckpoints, n int, dest string) string {
	count := pluralCount(n, nounCheckpoint, nounCheckpoints)
	switch {
	case info.PushDisabled && dest != "":
		return fmt.Sprintf("%s %s not on %s:", count, beVerb(n), dest)
	case info.PushDisabled:
		return fmt.Sprintf("%s %s not pushed:", count, beVerb(n))
	case info.Err != "":
		return fmt.Sprintf("'git push' sends none of these until that is fixed. %s %s queued:", count, beVerb(n))
	case dest == "":
		return fmt.Sprintf("%s %s queued, but no git remote is configured to send them to:", count, beVerb(n))
	case result.FirstPushDeferred:
		return fmt.Sprintf("%s %s not on %s yet. %s has no branches yet, so your next 'git push %s' sends only your branch, and these follow on the push after it:",
			count, beVerb(n), dest, dest, dest)
	case info.Source == checkpointSyncSourceDedicated:
		return fmt.Sprintf("%s %s to %s with your next 'git push':", count, goVerb(n), dest)
	default:
		return fmt.Sprintf("%s %s to %s with your next 'git push %s':", count, goVerb(n), dest, dest)
	}
}

func goVerb(n int) string {
	if n == 1 {
		return "goes"
	}
	return "go"
}

func beVerb(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func renderOutgoingRow(w io.Writer, row outgoingCheckpointRow) {
	name := row.CheckpointID.String()
	if name == "" {
		name = row.Ref.String()
	}
	header := tuiutil.SanitizeTerminalLabel(name)
	if row.err != nil {
		fmt.Fprintf(w, "%s  cannot read: %s\n\n", header, tuiutil.SanitizeTerminalLabel(row.err.Error()))
		return
	}
	if !row.createdAt.IsZero() {
		header += "  " + timeAgo(row.createdAt)
	}
	if row.agent != "" {
		header += "  " + tuiutil.SanitizeTerminalLabel(row.agent)
	}
	if row.Updated {
		header += "  updated"
	}
	fmt.Fprintln(w, header)
	switch {
	case row.prompt != "":
		fmt.Fprintf(w, "  prompt   %s\n", outgoingLabel(row.prompt))
	case row.summary != "":
		fmt.Fprintf(w, "  summary  %s\n", outgoingLabel(row.summary))
	}
	for _, f := range row.files {
		fmt.Fprintf(w, "  file     %s\n", tuiutil.SanitizeTerminalLabel(f))
	}
	fmt.Fprintln(w)
}

// outgoingLabel shortens free text to one line; --json carries it in full.
func outgoingLabel(s string) string {
	return tuiutil.SanitizeTerminalLabel(stringutil.TruncateRunes(stringutil.CollapseWhitespace(s), previewPromptWidth, "..."))
}

func renderStaleQueueEntries(w io.Writer, n int) {
	switch {
	case n == 1:
		fmt.Fprintln(w, "1 queued checkpoint ref no longer exists locally; the next push drops it from the queue.")
	case n > 1:
		fmt.Fprintf(w, "%d queued checkpoint refs no longer exist locally; the next push drops them from the queue.\n", n)
	}
}
