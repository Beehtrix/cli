package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/entireio/cli/internal/coreapi"
)

// Sync-block reasons the control plane states today. The wire field is an
// open string (MirrorSyncBlock.reason documents that more may be added), so
// anything else gets the generic rendering rather than a branch of its own.
const (
	syncBlockOrgIPAllowlist        = "org_ip_allowlist"
	syncBlockInstallationSuspended = "installation_suspended"
)

// mirrorSyncBlock is the --json shape of a GitHub installation block, on the
// repo row rather than on a placement: the block is per installation, so every
// placement of the upstream carries the same value and repeating it per row
// would say nothing the first copy did not.
type mirrorSyncBlock struct {
	Reason     string    `json:"reason"`
	ObservedAt time.Time `json:"observedAt"`
}

// mirrorSyncReader is what the block lookup reads: the repo's placements and
// one mirror record. *coreapi.Client satisfies it.
type mirrorSyncReader interface {
	ResolveMirrorPlacements(ctx context.Context, params coreapi.ResolveMirrorPlacementsParams) (*coreapi.ResolvePlacementsOutputBody, error)
	GetMirror(ctx context.Context, params coreapi.GetMirrorParams) (*coreapi.Mirror, error)
}

// lookupMirrorSyncBlock answers whether GitHub currently blocks the Entire
// installation serving one upstream, by reading that upstream's own mirror
// record: the pull-gated placement lookup names the mirror IDs, and the
// mirror read carries the block. Two exact reads, scoped to this repo —
// nothing is walked. Any one placement answers, since the block is per
// installation and every placement of the upstream carries the same value;
// GET /mirrors/{id} follows core's cross-cell redirect, so the first one
// listed is read wherever it lives.
//
// ref is the owner/repo as the user named it; the server matches the pair
// case-insensitively. blocked is false for "not blocked, as far as the caller
// can see": no placement the caller may pull, or a mirror carrying no block.
func lookupMirrorSyncBlock(ctx context.Context, c mirrorSyncReader, ref string) (block mirrorSyncBlock, blocked bool, err error) {
	owner, repo := mirrorRefOwner(ref), mirrorRefRepo(ref)
	placements, err := c.ResolveMirrorPlacements(ctx, coreapi.ResolveMirrorPlacementsParams{
		Provider: coreapi.ResolveMirrorPlacementsProviderGithub,
		Owner:    owner,
		Repo:     repo,
	})
	if err != nil {
		return mirrorSyncBlock{}, false, fmt.Errorf("resolve mirror placements: %w", err)
	}
	if len(placements.Placements) == 0 {
		return mirrorSyncBlock{}, false, nil
	}
	m, err := c.GetMirror(ctx, coreapi.GetMirrorParams{MirrorId: placements.Placements[0].MirrorId})
	if err != nil {
		return mirrorSyncBlock{}, false, fmt.Errorf("read mirror: %w", err)
	}
	b, ok := m.SyncBlock.Get()
	if !ok {
		return mirrorSyncBlock{}, false, nil
	}
	return mirrorSyncBlock{Reason: b.Reason, ObservedAt: b.ObservedAt}, true, nil
}

// mirrorRefRepo is the repo half of a /<forge>/<owner>/<repo> ref, the
// counterpart of mirrorRefOwner.
func mirrorRefRepo(ref string) string {
	trimmed := trimRefPrefix(ref)
	for _, forge := range []string{mirrorCloneForge, nativeCloneForge} {
		if rest, ok := strings.CutPrefix(trimmed, forge+"/"); ok {
			trimmed = rest
			break
		}
	}
	_, repo, _ := strings.Cut(trimmed, "/")
	return repo
}

// noteSyncStatusUnavailable decides what a failed block lookup means for the
// view it was made for. An interruption (the context cancelled or expired) is
// returned as the failure it is, so the command exits the way an interrupted
// command does instead of rendering a table over it. Any other failure is a
// note on stderr and the view goes on: the table it accompanies is still
// right about what it shows, so the command does not fail — but a reader
// checking why a mirror is stale must not take the absence of a block for
// its presence being ruled out.
func noteSyncStatusUnavailable(ctx context.Context, errW io.Writer, err error) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	fmt.Fprintf(errW, "Note: GitHub sync status unavailable: %v\n", renderCoreError(err))
	return nil
}

// syncBlockProblem says what is wrong, in terms the person who can fix it
// recognises. Only an unknown reason shows Entire's enum, since there is
// nothing else to say about it.
func syncBlockProblem(owner, reason string) string {
	switch reason {
	case syncBlockOrgIPAllowlist:
		return "The GitHub IP allow list of the " + owner + " organization blocks Entire, so its mirrors cannot sync from GitHub."
	case syncBlockInstallationSuspended:
		return "The Entire GitHub App installation for " + owner + " is suspended, so its mirrors cannot sync from GitHub."
	default:
		return "GitHub blocks the Entire app installation for " + owner + " (" + reason + "), so its mirrors cannot sync from GitHub."
	}
}

// syncBlockFix is the action, addressed to whoever administers the owner on
// GitHub: only they can take it, and a collaborator reading this is told whom
// to ask. An IP allow list is an organization feature; a suspension can also
// hit a personal account's installation, so that wording names no org.
func syncBlockFix(owner, reason string) string {
	switch reason {
	case syncBlockOrgIPAllowlist:
		return "Ask an admin of the " + owner + " organization to enable access for installed GitHub Apps in its IP allow list settings on GitHub."
	case syncBlockInstallationSuspended:
		return "Ask an admin of " + owner + " on GitHub to unsuspend the Entire app in its GitHub Apps settings."
	default:
		return "Ask an admin of " + owner + " on GitHub to restore the Entire app's access."
	}
}

// renderSyncBlock prints the block as a section of the `repo view` record:
// when it was last seen, what is wrong, what to do, and the command that
// confirms the fix — resync probes GitHub before it queues anything, so it is
// the check as well as the catch-up.
func renderSyncBlock(w io.Writer, st statusStyles, ref string, block *mirrorSyncBlock) {
	owner := mirrorRefOwner(ref)
	fmt.Fprintln(w, st.render(st.yellow, "Sync:"))
	label := "Blocked"
	if !block.ObservedAt.IsZero() {
		label += " (observed " + timeAgo(block.ObservedAt) + ")"
	}
	fmt.Fprintf(w, "  %s %s\n", st.render(st.red, label+":"), syncBlockProblem(owner, block.Reason))
	fmt.Fprintf(w, "  %s Then run `entire repo mirror resync %s` to confirm the fix and catch the mirror up.\n", syncBlockFix(owner, block.Reason), ref)
}

// syncBlockDetailLocation is where the server attaches the block reason on
// the 409 a resync answers while GitHub still blocks the installation.
const syncBlockDetailLocation = "body.owner"

func newRepoMirrorResyncCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resync <repo>",
		Short: "Queue a fresh sync of a GitHub mirror on every cluster",
		Long: "Queue a fresh sync of a GitHub mirror on every cluster it is placed on, " +
			"so a mirror that has fallen behind its upstream catches up without " +
			"waiting for the next GitHub event.\n\n" +
			"This is also the check after fixing a block. When GitHub blocks the " +
			"Entire app installation for the repository's owner (an org IP " +
			"allow list, or a suspended installation — `entire repo view` shows " +
			"which), the server probes GitHub first: if the block has been lifted " +
			"it clears the block and queues the sync; if not, nothing is queued and " +
			"the command reports what still needs fixing.\n\n" +
			"Requires write access to the GitHub repository. Serves /gh/ references " +
			"only: an Entire-native repository has no upstream to sync from.\n\n" +
			"--json prints the server's response (mirrorId, status) plus the repo.",
		Example: "  entire repo mirror resync /gh/octocat/hello-world\n" +
			"  entire repo mirror resync /gh/octocat/hello-world --json",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, err := parseMirrorRepoRef(args[0], mirrorCloneForge)
			if err != nil {
				return err
			}
			return runCore(cmd, func(ctx context.Context, c *coreapi.Client) error {
				out, err := c.ResyncMirror(ctx, &coreapi.ResyncMirrorInputBody{
					Provider: coreapi.ResyncMirrorInputBodyProviderGithub,
					Owner:    ref.owner,
					Repo:     ref.repo,
				})
				if err != nil {
					return resyncError(ref, err)
				}
				if jsonRequested(cmd) {
					// The wire object plus the repo it was asked about, so a
					// field the server adds later reaches the reader unchanged.
					obj, err := mergeSynthesizedField(out, "repo", ref.qualified)
					if err != nil {
						return err
					}
					return printJSON(cmd.OutOrStdout(), obj)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Resync %s for %s on every cluster it is mirrored on.\n", out.Status, ref.qualified())
				return nil
			})
		},
	}
	addJSONFlag(cmd)
	return cmd
}

// resyncError turns the server's refusal into the message a reader can act
// on. A 409 is the one coded case: GitHub still blocks the installation, the
// server has re-stamped the block, and the reason rides in a problem detail
// at body.owner — rendered with the same words `repo view` uses, so the two
// never describe one block two ways. Everything else keeps the server's own
// message (renderCoreError, via runCore).
func resyncError(ref mirrorRepoRef, err error) error {
	if coreapi.APIErrorStatus(err) != http.StatusConflict {
		return err
	}
	detail, ok := coreapi.APIErrorDetail(err, syncBlockDetailLocation)
	if !ok {
		return err
	}
	var reason string
	if jerr := json.Unmarshal(detail.Value, &reason); jerr != nil || strings.TrimSpace(reason) == "" {
		return err
	}
	return errors.New(syncBlockProblem(ref.owner, reason) + " Nothing was queued.\n" +
		syncBlockFix(ref.owner, reason) + " Then run this command again.")
}
