package strategy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/remote"
	"github.com/entireio/cli/cmd/entire/cli/gitdir"
	"github.com/entireio/cli/cmd/entire/cli/interactive"
	"github.com/entireio/cli/cmd/entire/cli/internal/flock"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/settings"

	"github.com/go-git/go-git/v6/plumbing"
)

// checkpointRemoteFetchTimeout bounds checkpoint-remote fetches made from the
// push hot path, where the user's own `git push` is blocked for the duration.
const checkpointRemoteFetchTimeout = 30 * time.Second

// checkpointFetchMode selects how a checkpoint-remote fetch is bounded.
type checkpointFetchMode int

const (
	// hotPathFetch bounds the fetch by checkpointRemoteFetchTimeout.
	hotPathFetch checkpointFetchMode = iota
	// foregroundFetch is for user-initiated commands (enable, resume, explain,
	// delete), which fetch the whole transcript archive. No fixed deadline
	// suits that: one the archive outgrows discards the partial transfer, the
	// ref never lands, and every later command restarts the same fetch from
	// zero. So these run until done and abort only a stalled transfer, under a
	// per-repository lock so concurrent commands transfer the archive once.
	foregroundFetch
)

// checkpointRemoteFetchLockName is the lock file, in the git common dir, that
// serializes foreground checkpoint-remote fetches across processes.
const checkpointRemoteFetchLockName = "entire-checkpoint-remote-fetch.lock"

// checkpointRemoteFetchLockWait bounds how long a foreground fetch waits for
// another to finish before fetching unserialized. It is well above the ~11
// minutes a multi-gigabyte archive took to transfer, so a working fetch is
// waited out, while one that hangs cannot block every later command forever.
const checkpointRemoteFetchLockWait = 30 * time.Minute

// pushSettings holds the resolved push configuration from a single settings load.
type pushSettings struct {
	// remote is the git remote name to use for pushing (the user's push remote).
	remote string
	// checkpointURL is the derived URL for pushing checkpoint branches.
	// When set, checkpoint/trails branches are pushed directly to this URL
	// instead of the remote name. Empty means use the remote name.
	checkpointURL string
	// pushDisabled is true if push_sessions is explicitly set to false.
	pushDisabled bool
	// checkpointRemoteConfigured records that settings NAME a checkpoint_remote,
	// which checkpointURL alone cannot express: an empty checkpointURL means
	// either "none configured" or "configured and refused", and only the second
	// is worth telling the user about.
	checkpointRemoteConfigured bool
	// primaryIsRefs records whether the git-refs backend is the configured
	// primary, resolved once here so the pre-push path does not re-read the
	// checkpoints config (LoadCheckpointsConfig is uncached: two whole-file
	// reads and JSON parses per call).
	primaryIsRefs bool
}

// pushTarget returns the target to use for git push/fetch commands for checkpoint branches.
// If a checkpoint URL is configured, returns that; otherwise returns the remote name.
func (ps *pushSettings) pushTarget() string {
	if ps.checkpointURL != "" {
		return ps.checkpointURL
	}
	return ps.remote
}

// hasCheckpointURL returns true if a dedicated checkpoint URL is configured.
func (ps *pushSettings) hasCheckpointURL() bool {
	return ps.checkpointURL != ""
}

// resolvePushSettings loads settings once and returns the resolved push config.
// If a structured checkpoint_remote is configured (e.g., {"provider": "github", "repo": "org/repo"}):
//   - Derives the checkpoint URL from the push remote's protocol (SSH vs HTTPS)
//   - Ignores the setting when it looks inherited from an upstream project rather
//     than configured by this developer, so a fork contributor's checkpoints are
//     not pushed into the upstream's checkpoint repo (see
//     remote.checkpointRemoteIsInherited for how ownership is established)
//   - If a checkpoint branch doesn't exist locally, attempts to fetch it from the URL
//
// The push itself handles failures gracefully (doPushRef warns and continues),
// so no reachability check is needed here.
func resolvePushSettings(ctx context.Context, pushRemoteName string) pushSettings {
	s, err := settings.Load(ctx)
	if err != nil {
		return pushSettings{remote: pushRemoteName}
	}

	ps := pushSettings{
		remote:        pushRemoteName,
		pushDisabled:  s.IsPushSessionsDisabled(),
		primaryIsRefs: primaryIsGitRefs(ctx),
	}

	config := s.GetCheckpointRemote()
	if config == nil {
		return ps
	}
	ps.checkpointRemoteConfigured = true
	checkpointURL, enabled, err := remote.PushURL(ctx, pushRemoteName)
	if err != nil {
		logging.Warn(ctx, "checkpoint-remote: could not derive URL from push remote",
			slog.String("remote", pushRemoteName),
			slog.String("repo", config.Repo),
			slog.String("error", err.Error()),
		)
		return ps
	}
	if !enabled || checkpointURL == "" {
		return ps
	}

	ps.checkpointURL = checkpointURL

	// If the v1 checkpoint branch doesn't exist locally, try to fetch it from the URL.
	// This is a one-time operation — once the branch exists locally, subsequent pushes
	// skip the fetch entirely. Only fetch the metadata branch; trails are always pushed
	// to the user's push remote, not the checkpoint remote.
	//
	// Skipped entirely under the git-refs primary backend, where the "one-time"
	// framing does not hold: that backend pushes per-checkpoint refs and never
	// writes the local v1 branch (see prePush), so the branch stays missing
	// forever and every single push would re-pay the fetch. On a large checkpoint
	// remote that is a multi-second stall on each `git push` for a branch this
	// push will not touch.
	if !ps.primaryIsRefs {
		if err := fetchMetadataBranchIfMissing(ctx, checkpointURL); err != nil {
			logging.Warn(ctx, "checkpoint-remote: failed to fetch metadata branch",
				slog.String("error", err.Error()),
			)
		}
	}

	return ps
}

// FetchMetadataBranch fetches the metadata branch from the checkpoint remote URL
// and updates the local branch. Unlike fetchMetadataBranchIfMissing, this always
// fetches regardless of whether the branch exists locally (for resume scenarios
// where the local branch may be stale).
//
// The fetch is unfiltered (NoFilter: true) because resume needs blob content
// (transcripts, metadata JSON) — not just tree objects, and it runs as a
// foregroundFetch for the same reason: it is the call that actually moves the
// transcript archive.
func FetchMetadataBranch(ctx context.Context, remoteURL string) error {
	return fetchMetadataBranchWithin(ctx, remoteURL, foregroundFetch)
}

func fetchMetadataBranchWithin(ctx context.Context, remoteURL string, mode checkpointFetchMode) error {
	refs := checkpoint.ResolveRefs(ctx)
	if !refs.Primary.IsBranch() {
		return fmt.Errorf("primary metadata ref %s is not a branch", refs.Primary)
	}
	branchName := refs.Primary.Short()
	tmpRef := FetchTmpRefPrefix + branchName
	if mode == hotPathFetch {
		// The hot path skips the fetch lock rather than stall a push behind a
		// long foreground fetch, so it must not share that fetch's tmp ref.
		tmpRef = FetchTmpRefPrefix + "push/" + branchName
	}
	srcRef := refs.Primary.String()

	if mode == foregroundFetch {
		release, err := acquireCheckpointRemoteFetchLock(ctx, "")
		if err != nil {
			return err
		}
		defer release()
	}
	if err := fetchURLIntoTmpRef(ctx, "", remoteURL, srcRef, tmpRef, "metadata branch", true, mode); err != nil {
		return err
	}
	if err := PromoteTmpRefSafely(ctx, plumbing.ReferenceName(tmpRef), refs.Primary, branchName); err != nil {
		return err
	}

	return nil
}

// fetchURLIntoTmpRef runs `git fetch <remoteURL> +<srcRef>:<tmpRef>` via the
// checkpoint git wrapper, disabling the terminal prompt so a misconfigured
// credential helper doesn't hang the process. Errors include the redacted URL
// and any captured stderr so operators can diagnose without credentials
// leaking into logs.
//
// When noFilter is true, --filter=blob:none is suppressed even if filtered
// fetches are globally enabled. v1's blobs are the full transcript archive, so
// an unfiltered fetch costs the whole history where a filtered one costs only
// the commit graph — but every caller here passes true, and the reason is worth
// stating because the cheaper option looks safe and is not.
//
// Filtering would be correct only where nothing downstream reads blob content
// from the local branch. No caller meets that bar: each one lands v1 as the
// repo's checkpoint store, and GitStore.List reads each checkpoint's
// metadata.json through a plain tree with no blob fetcher attached. Worse, the
// read path's recovery tier is keyed on the *ref* being missing, so once a
// filtered fetch lands the ref it never fires again — leaving `checkpoint list`
// permanently showing bare IDs with no prompt, date, or counts, recoverable only
// by explaining each checkpoint by ID.
//
// The parameter is kept rather than inlined so the contract stays explicit at
// each call site.
// mode bounds the fetch: hotPathFetch on the push hot path, foregroundFetch from
// user-initiated commands.
func fetchURLIntoTmpRef(ctx context.Context, dir, remoteURL, srcRef, tmpRef, label string, noFilter bool, mode checkpointFetchMode) error { //nolint:unparam // every caller needs blob content today; see the doc above for why the filtered variant is unsafe here
	opts := remote.FetchOptions{
		Remote:   remoteURL,
		RefSpecs: []string{fmt.Sprintf("+%s:%s", srcRef, tmpRef)},
		NoTags:   true,
		NoFilter: noFilter,
		Dir:      dir,
	}
	fetchCtx, cancel := boundFetch(ctx, mode, &opts)
	defer cancel()
	if mode == foregroundFetch {
		printFetchStatus("Fetching checkpoints from " + remote.RedactURLOrPath(remoteURL))
	}

	_, fetchErr := remote.Fetch(fetchCtx, opts)
	if fetchErr == nil {
		return nil
	}

	// remote.Fetch already folds git's output into fetchErr, redacted and
	// capped; splicing it again printed the same text twice.
	redactedURL := remote.RedactURL(remoteURL)
	return fmt.Errorf("fetch %s from %s failed: %w", label, redactedURL, fetchErr)
}

// printFetchStatus tells a terminal user why a foreground command is paused.
// These fetches run under spinners owned by the calling command, so on a
// terminal that renders ANSI the line is cleared first: the message takes the
// spinner's row and the spinner redraws below it.
func printFetchStatus(msg string) {
	switch {
	case interactive.ShouldStyle(os.Stderr):
		fmt.Fprintf(os.Stderr, "\r\033[K[entire] %s\n", msg)
	case interactive.IsTerminalWriter(os.Stderr):
		fmt.Fprintf(os.Stderr, "[entire] %s\n", msg)
	}
}

// boundFetch applies mode to a fetch, returning the context it runs under.
func boundFetch(ctx context.Context, mode checkpointFetchMode, opts *remote.FetchOptions) (context.Context, context.CancelFunc) {
	if mode == foregroundFetch {
		opts.AbortStalled = true
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, checkpointRemoteFetchTimeout)
}

// acquireCheckpointRemoteFetchLock waits for any other foreground
// checkpoint-remote fetch in this repository to finish. The waiter's own fetch
// then negotiates against the objects the first one landed and transfers only
// what is new.
//
// Callers that fetch into the shared metadata tmp ref (FetchTmpRefPrefix plus
// the primary branch) hold the lock until that ref is promoted and removed:
// released after the fetch alone, a finishing caller's cleanup can delete the
// ref out from under the next caller's fetch. The push hot path does not take
// the lock and uses its own tmp ref instead.
//
// The wait says why the command is paused, stops when ctx is cancelled, and
// gives up after checkpointRemoteFetchLockWait. Giving up, or failing to take
// the lock at all, logs and lets the caller fetch unserialized; only ctx's own
// error is returned. dir is the worktree to resolve the git common dir from;
// empty means the current repository.
func acquireCheckpointRemoteFetchLock(ctx context.Context, dir string) (release func(), err error) {
	var root *os.Root
	if dir == "" {
		root, err = gitdir.Open(ctx)
	} else {
		var commonDir string
		if commonDir, err = gitdir.CommonDirForWorktree(ctx, dir); err == nil {
			root, err = gitdir.OpenAt(commonDir)
		}
	}
	if err == nil {
		release, err = acquireFetchLockIn(ctx, root)
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("wait for checkpoint fetch lock: %w", ctxErr)
		}
		logging.Warn(ctx, "checkpoint-remote: fetch lock unavailable; fetching unserialized",
			slog.String("error", err.Error()),
		)
		return func() {}, nil
	}
	return release, nil
}

// acquireFetchLockIn takes the fetch lock in root, announcing the wait when
// another process holds it. flock only polls, and so only honors cancellation,
// under a deadline: an already-expired one makes the first attempt a
// non-blocking probe.
func acquireFetchLockIn(ctx context.Context, root *os.Root) (func(), error) {
	probeCtx, cancelProbe := context.WithDeadline(ctx, time.Now())
	release, err := flock.AcquireContextIn(probeCtx, root, checkpointRemoteFetchLockName)
	cancelProbe()
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		return release, err //nolint:wrapcheck // flock errors are already wrapped
	}

	printFetchStatus("Waiting for another entire command to finish fetching checkpoints...")
	logging.Info(ctx, "checkpoint-remote: waiting for another fetch to release the fetch lock")
	waitCtx, cancel := context.WithTimeout(ctx, checkpointRemoteFetchLockWait)
	defer cancel()
	return flock.AcquireContextIn(waitCtx, root, checkpointRemoteFetchLockName) //nolint:wrapcheck // flock errors are already wrapped
}

// fetchMetadataBranchIfMissing fetches the primary metadata ref from a URL only if it doesn't exist locally.
// This avoids network calls on every push — once the branch exists locally, this is a no-op.
// It runs on the push hot path, so it uses the short fetch budget.
//
// A fetch failure is not fatal — the push will create the branch on the remote
// when it succeeds — but it is returned rather than swallowed so the caller can
// log it. Returning nil unconditionally meant a checkpoint remote that was
// unreachable, too slow, or refusing auth looked identical to one that was never
// contacted, and resolvePushSettings' warning could never fire.
//
// A remote that simply does not carry the branch yet (the normal state of a
// brand-new checkpoint repo) is not a failure and stays quiet. That case is
// established by probing with ls-remote first — positive evidence, matching
// remote.FetchCheckpointRef's absence-vs-failure contract — rather than by
// treating every fetch error as absence.
//
// The probe and the fetch share one deadline, so the budget the caller sees is
// the constant's value rather than twice it: per-call timeouts do not compose
// when applied at two nesting levels.
func fetchMetadataBranchIfMissing(ctx context.Context, remoteURL string) error {
	repo, err := OpenRepository(ctx)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}
	defer repo.Close()

	// Check if branch already exists locally - if so, nothing to do. Only a
	// genuinely-absent ref means "missing": any other read failure (corrupt or
	// unreadable ref storage) is surfaced rather than masked as absence, which
	// would send us to the network to fix a local problem.
	refs := checkpoint.ResolveRefs(ctx)
	switch _, err := repo.Reference(refs.Primary, true); {
	case err == nil:
		return nil // Branch exists locally, skip fetch
	case !errors.Is(err, plumbing.ErrReferenceNotFound):
		return fmt.Errorf("read local ref %s: %w", refs.Primary, err)
	}

	ctx, cancel := context.WithTimeout(ctx, checkpointRemoteFetchTimeout)
	defer cancel()

	out, probeErr := remote.LsRemoteInDir(ctx, "", remoteURL, refs.Primary.String())
	if probeErr != nil {
		return fmt.Errorf("probe %s on %s: %w", refs.Primary, remote.RedactURL(remoteURL), probeErr)
	}
	if len(bytes.TrimSpace(out)) == 0 {
		// Remote reachable, branch not there yet. The first push creates it.
		return nil
	}

	if err := fetchMetadataBranchWithin(ctx, remoteURL, hotPathFetch); err != nil {
		return err
	}

	logging.Info(ctx, "checkpoint-remote: fetched metadata branch from URL")
	return nil
}

// resolveCheckpointFetchURL resolves the checkpoint_remote fetch URL for the repo
// rooted at worktreeRoot and verifies it actually targets the configured
// checkpoint repository. It returns ok=false (never an error) when no
// checkpoint_remote is configured or a dedicated checkpoint URL cannot be
// resolved, so callers fall back to keeping the local branch / creating an orphan.
//
// remote.FetchURL silently falls back to the origin remote URL in several paths
// (ENTIRE_CHECKPOINT_TOKEN short-circuit, unparseable origin, non-derivable origin
// protocol). Adopting from origin would contradict the rule that origin is never
// authoritative when a checkpoint_remote is configured (issue #1374), so a resolved
// URL that does not target the configured checkpoint repo is treated as unresolved.
func resolveCheckpointFetchURL(ctx context.Context, worktreeRoot string) (string, bool) {
	s, err := settings.Load(ctx)
	if err != nil {
		logging.Debug(ctx, "checkpoint-remote: could not load settings for metadata fetch URL",
			slog.String("error", err.Error()))
		return "", false
	}
	config := s.GetCheckpointRemote()
	if config == nil {
		return "", false
	}
	url, err := remote.FetchURL(ctx, remote.FetchURLOptions{
		WorktreeRoot: worktreeRoot,
		// The elected remote joins the ownership vote so the fork-shaped
		// topology that only the push destination exposes is refused here too.
		LeadReadRemote: LeadCheckpointReadRemote(ctx),
	})
	if err != nil || strings.TrimSpace(url) == "" {
		logging.Debug(ctx, "checkpoint-remote: could not resolve fetch URL for metadata bootstrap",
			slog.Any("error", err))
		return "", false
	}
	if !urlTargetsCheckpointRepo(url, config) {
		logging.Debug(ctx, "checkpoint-remote: fetch URL did not resolve to the configured checkpoint repo; not adopting from origin",
			slog.String("repo", config.Repo))
		return "", false
	}
	return url, true
}

// urlTargetsCheckpointRepo reports whether url points at the configured checkpoint
// repository (host-agnostic, case-insensitive owner/repo match). It distinguishes a
// derived checkpoint URL from remote.FetchURL's origin fallback, which targets the
// origin repository instead. A same-repo checkpoint_remote (origin == checkpoint
// repo) still matches, which is correct: adopting from that URL is adopting the
// checkpoint repo.
func urlTargetsCheckpointRepo(url string, config *settings.CheckpointRemoteConfig) bool {
	info, err := remote.ParseURL(url)
	if err != nil || info.Owner == "" || info.Repo == "" {
		return false
	}
	return strings.EqualFold(info.Owner+"/"+info.Repo, config.Repo)
}

// CheckpointPushTarget returns where PrePush sends checkpoint metadata for
// pushRemoteName — the checkpoint_remote URL when one is configured, otherwise
// the remote itself — and whether push_sessions turns pushing off. Callers use
// it to confirm delivery after a push, since PrePush is deliberately fail-soft.
func CheckpointPushTarget(ctx context.Context, pushRemoteName string) (target string, disabled bool) {
	ps := resolvePushSettings(ctx, pushRemoteName)
	if ps.checkpointURL != "" {
		return ps.checkpointURL, ps.pushDisabled
	}
	return ps.remote, ps.pushDisabled
}
