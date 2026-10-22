package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
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

// syncBlockPageSize is the most GET /mirrors returns per page (the spec's
// maximum). The walk wants as few round-trips as the server allows: its cost
// is per page, not per row.
const syncBlockPageSize = 500

// lookupSyncBlocks answers, for each owner asked about, whether GitHub
// currently blocks the Entire installation serving it. The block is stamped
// per installation and an installation is per GitHub account, so the first
// mirror of an owner the caller can see answers for every repo of that owner.
//
// It is one walk of GET /mirrors for all owners at once, deliberately with no
// owner filter: the server applies its filters AFTER cutting each page from
// the caller's whole visible set, so a filtered request still walks every
// page and merely returns most of them empty — and a one-entry page filtered
// to one owner is usually empty, which read as "not blocked". The walk stops
// as soon as every wanted owner has been seen, blocked or not; an owner never
// seen has no mirror visible to the caller and is reported unblocked.
//
// Keys of the result are the owners as asked; matching against the wire is
// case-insensitive, as the server's own owner filter is.
func lookupSyncBlocks(ctx context.Context, c mirrorLister, owners []string) (map[string]*mirrorSyncBlock, error) {
	blocks := make(map[string]*mirrorSyncBlock, len(owners))
	if len(owners) == 0 {
		return blocks, nil
	}
	pending := slices.Clone(owners)
	cursor := ""
	for len(pending) > 0 {
		params := coreapi.ListMirrorsParams{
			Provider: coreapi.NewOptString(repoProviderGitHub),
			PageSize: coreapi.NewOptInt32(syncBlockPageSize),
		}
		if cursor != "" {
			params.PageToken = coreapi.NewOptString(cursor)
		}
		out, err := c.ListMirrors(ctx, params)
		if err != nil {
			return nil, fmt.Errorf("read mirrors: %w", err)
		}
		for _, m := range out.Mirrors {
			i := slices.IndexFunc(pending, func(o string) bool { return strings.EqualFold(o, m.Owner) })
			if i < 0 {
				continue
			}
			if b, ok := m.SyncBlock.Get(); ok {
				blocks[pending[i]] = &mirrorSyncBlock{Reason: b.Reason, ObservedAt: b.ObservedAt}
			}
			pending = slices.Delete(pending, i, i+1)
		}
		cursor = out.NextPageToken.Or("")
		if cursor == "" {
			break
		}
	}
	return blocks, nil
}

// annotateSyncBlocks stamps each GitHub-mirror row with its owner's
// installation block. Candidate rows (not yet mirrored, so nothing to sync)
// and native rows (no GitHub installation) are skipped, and a set with no
// row to stamp costs no request at all.
func annotateSyncBlocks(ctx context.Context, c mirrorLister, rows []repoDirRow) error {
	var owners []string
	for _, r := range rows {
		if !syncBlockApplies(r) {
			continue
		}
		if owner := mirrorRefOwner(r.Repo); owner != "" && !slices.Contains(owners, owner) {
			owners = append(owners, owner)
		}
	}
	blocks, err := lookupSyncBlocks(ctx, c, owners)
	if err != nil {
		return err
	}
	for i := range rows {
		if syncBlockApplies(rows[i]) {
			rows[i].SyncBlock = blocks[mirrorRefOwner(rows[i].Repo)]
		}
	}
	return nil
}

// syncBlockApplies says whether a row can carry a block at all: a mirrored
// GitHub repo, and nothing else.
func syncBlockApplies(r repoDirRow) bool {
	return len(r.Placements) > 0 && declaresForge(r.Repo, mirrorCloneForge)
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

// syncBlockQualifier is appended to the STATUS cell of a row whose owner's
// installation is blocked. The placement status stays the clone lifecycle
// (the server's choice, and what --status filters on); the qualifier says the
// clone is not being kept current, which that lifecycle cannot.
const syncBlockQualifier = " (sync blocked)"

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
