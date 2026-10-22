package strategy

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/filemode"
	"github.com/go-git/go-git/v6/plumbing/object"
)

// CountUnpushedCheckpoints approximates how many checkpoints exist locally
// but not on the checkpoint sync remote. Fully local — no network.
// git-refs primary: push-queue length. git-branch primary: v1 commits ahead
// of refs/remotes/<remote>/<v1>; an absent tracking ref with a local v1
// branch counts every v1 commit (correct for the deferred-publish case; a
// stale tracking ref overcounts — acceptable for a local, no-network
// heuristic).
func CountUnpushedCheckpoints(ctx context.Context, remoteName string) (int, error) {
	if cpCfg, _ := settings.LoadCheckpointsConfig(ctx); checkpoint.PrimaryIsRefs(cpCfg) { //nolint:errcheck // fail-soft like prePush: a bad checkpoints block defaults to the git-branch backend
		return countQueuedCheckpointRefs(ctx)
	}
	return countUnpushedV1Commits(ctx, remoteName)
}

// countQueuedCheckpointRefs returns the push-discovery queue length (git-refs
// backend): every queued ref is a checkpoint written locally but not yet
// confirmed pushed.
func countQueuedCheckpointRefs(ctx context.Context) (int, error) {
	repo, err := OpenRepository(ctx)
	if err != nil {
		return 0, fmt.Errorf("open repository: %w", err)
	}
	defer repo.Close()

	refs, err := peekCheckpointQueue(ctx, repo)
	if err != nil {
		return 0, err
	}
	return len(refs), nil
}

// peekCheckpointQueue reads the push-discovery queue without compacting or
// pruning it; the push owns both.
func peekCheckpointQueue(ctx context.Context, repo *git.Repository) ([]plumbing.ReferenceName, error) {
	queue, err := checkpoint.PushQueueForRepo(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("resolve push queue: %w", err)
	}
	refs, err := queue.Peek()
	if err != nil {
		return nil, fmt.Errorf("read push queue: %w", err)
	}
	return refs, nil
}

// countUnpushedV1Commits counts v1-branch commits not on the remote-tracking
// ref (git-branch backend). No local v1 branch means nothing to push (0).
func countUnpushedV1Commits(ctx context.Context, remoteName string) (int, error) {
	local := checkpoint.ResolveRefs(ctx).Primary
	if !gitCommitRefExists(ctx, local.String()) {
		return 0, nil
	}
	rangeSpec := local.String()
	if remoteName != "" {
		tracking := "refs/remotes/" + remoteName + "/" + local.Short()
		if gitCommitRefExists(ctx, tracking) {
			rangeSpec = tracking + ".." + local.String()
		}
	}
	out, err := exec.CommandContext(ctx, "git", "rev-list", "--count", rangeSpec).Output()
	if err != nil {
		return 0, fmt.Errorf("count unpushed v1 commits: %w", err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("parse rev-list count: %w", err)
	}
	return n, nil
}

// gitCommitRefExists reports whether ref resolves to a commit in the current
// repo. Local and best-effort: any error reads as "absent".
func gitCommitRefExists(ctx context.Context, ref string) bool {
	return exec.CommandContext(ctx, "git", "rev-parse", "--verify", "--quiet", ref+"^{commit}").Run() == nil
}

// OutgoingCheckpoint is one checkpoint the next push to the checkpoint sync
// destination sends.
type OutgoingCheckpoint struct {
	// CheckpointID is empty for a queued ref that exists locally but does not
	// name a checkpoint. The push sends it anyway.
	CheckpointID id.CheckpointID
	// Ref is the per-checkpoint ref on git-refs, empty on git-branch.
	Ref plumbing.ReferenceName
	// Updated reports that an earlier version exists. On git-branch the
	// destination's tracking ref already holds the checkpoint. On git-refs the
	// ref has been written more than once, and nothing local records whether
	// an earlier write was pushed.
	Updated bool
}

// OutgoingCheckpoints is what the next push to the checkpoint sync destination
// sends, as far as local state can tell.
type OutgoingCheckpoints struct {
	PrimaryIsRefs bool
	// Known is false when nothing local records what the destination holds
	// (git-branch with no named remote to compare against). Checkpoints is
	// then empty rather than a guess.
	Known       bool
	Checkpoints []OutgoingCheckpoint
	// StaleQueueEntries counts queued refs missing locally. The next push
	// drops them from the queue instead of sending them (git-refs).
	StaleQueueEntries int
	// FirstPushDeferred reports that the destination has no branches yet, so
	// the next push sends only the user's branch (git-branch).
	FirstPushDeferred bool
}

// ListOutgoingCheckpoints reports what the next push to remoteName, the elected
// checkpoint sync remote, sends. remoteName is empty for a dedicated
// checkpoint_remote and when no remote is elected. It reads local refs only:
// no fetch, no ls-remote.
func ListOutgoingCheckpoints(ctx context.Context, remoteName string) (OutgoingCheckpoints, error) {
	repo, err := OpenRepository(ctx)
	if err != nil {
		return OutgoingCheckpoints{}, fmt.Errorf("open repository: %w", err)
	}
	defer repo.Close()

	if primaryIsGitRefs(ctx) {
		return listQueuedCheckpointRefs(ctx, repo)
	}
	return listUnpushedV1Checkpoints(ctx, repo, remoteName)
}

// listQueuedCheckpointRefs lists the queue the way the push consumes it: refs
// missing locally are pruned rather than sent, and every other queued ref is
// sent whether or not its name parses.
func listQueuedCheckpointRefs(ctx context.Context, repo *git.Repository) (OutgoingCheckpoints, error) {
	queued, err := peekCheckpointQueue(ctx, repo)
	if err != nil {
		return OutgoingCheckpoints{}, err
	}
	existing, stale := partitionLocalRefs(repo, queued)
	out := OutgoingCheckpoints{
		PrimaryIsRefs:     true,
		Known:             true,
		Checkpoints:       make([]OutgoingCheckpoint, 0, len(existing)),
		StaleQueueEntries: len(stale),
	}
	for _, ref := range existing {
		cid, _ := checkpoint.ParseRef(ref)
		out.Checkpoints = append(out.Checkpoints, OutgoingCheckpoint{
			CheckpointID: cid,
			Ref:          ref,
			Updated:      refWrittenMoreThanOnce(repo, ref),
		})
	}
	return out, nil
}

// refWrittenMoreThanOnce reports whether a checkpoint ref's tip has a parent:
// the first write is an orphan commit and every later one parents on the tip.
func refWrittenMoreThanOnce(repo *git.Repository, ref plumbing.ReferenceName) bool {
	resolved, err := repo.Reference(ref, true)
	if err != nil {
		return false
	}
	commit, err := repo.CommitObject(resolved.Hash())
	if err != nil {
		return false
	}
	return commit.NumParents() > 0
}

// listUnpushedV1Checkpoints lists the checkpoints local v1 changed since its
// merge base with remoteName's tracking ref: the commits a push of v1 sends,
// including the ones divergence recovery replays. No tracking ref means the
// push sends all of v1. A tracking ref behind the remote over-lists; the push
// trusts the same ref to decide whether v1 needs pushing at all.
func listUnpushedV1Checkpoints(ctx context.Context, repo *git.Repository, remoteName string) (OutgoingCheckpoints, error) {
	out := OutgoingCheckpoints{Checkpoints: []OutgoingCheckpoint{}}
	if remoteName == "" {
		return out, nil
	}
	out.Known = true

	localRef := checkpoint.ResolveRefs(ctx).Primary
	local, err := refCommit(repo, localRef)
	if errors.Is(err, plumbing.ErrReferenceNotFound) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	tracking, err := refCommit(repo, plumbing.NewRemoteReferenceName(remoteName, localRef.Short()))
	if err != nil && !errors.Is(err, plumbing.ErrReferenceNotFound) {
		return out, err
	}

	localTree, err := local.Tree()
	if err != nil {
		return out, fmt.Errorf("read %s tree: %w", localRef.Short(), err)
	}
	var baseTree, trackingTree *object.Tree
	if tracking != nil {
		if trackingTree, err = tracking.Tree(); err != nil {
			return out, fmt.Errorf("read tracking tree: %w", err)
		}
		bases, err := local.MergeBase(tracking)
		if err != nil {
			return out, fmt.Errorf("merge base of %s and its tracking ref: %w", localRef.Short(), err)
		}
		// Unrelated or criss-cross histories leave no single base: list all of
		// v1 rather than risk leaving out something the push sends.
		if len(bases) == 1 {
			if baseTree, err = bases[0].Tree(); err != nil {
				return out, fmt.Errorf("read merge base tree: %w", err)
			}
		}
	}

	changed, err := changedV1Checkpoints(localTree, baseTree)
	if err != nil {
		return out, err
	}
	for _, cid := range changed {
		updated := false
		if trackingTree != nil {
			_, findErr := trackingTree.FindEntry(cid.Path())
			updated = findErr == nil
		}
		out.Checkpoints = append(out.Checkpoints, OutgoingCheckpoint{CheckpointID: cid, Updated: updated})
	}
	// The same local check deferCheckpointPushOnEmptyRemote makes for a push
	// from the git hook.
	if len(out.Checkpoints) > 0 {
		out.FirstPushDeferred = isConfiguredRemote(ctx, remoteName) && !remoteHasTrackingRefs(ctx, remoteName)
	}
	return out, nil
}

// refCommit resolves ref to its commit. A missing ref is an error wrapping
// plumbing.ErrReferenceNotFound.
func refCommit(repo *git.Repository, ref plumbing.ReferenceName) (*object.Commit, error) {
	resolved, err := repo.Reference(ref, true)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", ref, err)
	}
	commit, err := repo.CommitObject(resolved.Hash())
	if err != nil {
		return nil, fmt.Errorf("read %s commit: %w", ref, err)
	}
	return commit, nil
}

// changedV1Checkpoints lists the checkpoints whose folder in local differs from
// base, or every checkpoint in local when base is nil. v1 stores a checkpoint
// at <id[:2]>/<id[2:]>/, so comparing subtree hashes two levels down finds them
// without reading checkpoint content.
func changedV1Checkpoints(local, base *object.Tree) ([]id.CheckpointID, error) {
	var changed []id.CheckpointID
	for _, shard := range local.Entries {
		if shard.Mode != filemode.Dir {
			continue
		}
		var baseShard *object.Tree
		if base != nil {
			if entry, err := base.FindEntry(shard.Name); err == nil {
				if entry.Hash.Equal(shard.Hash) {
					continue
				}
				if entry.Mode == filemode.Dir {
					var err error
					if baseShard, err = base.Tree(shard.Name); err != nil {
						return nil, fmt.Errorf("read base shard %s: %w", shard.Name, err)
					}
				}
			}
		}
		shardTree, err := local.Tree(shard.Name)
		if err != nil {
			return nil, fmt.Errorf("read shard %s: %w", shard.Name, err)
		}
		for _, folder := range shardTree.Entries {
			if folder.Mode != filemode.Dir {
				continue
			}
			if baseShard != nil {
				if entry, err := baseShard.FindEntry(folder.Name); err == nil && entry.Hash.Equal(folder.Hash) {
					continue
				}
			}
			cid, err := id.NewCheckpointID(shard.Name + folder.Name)
			if err != nil {
				continue
			}
			changed = append(changed, cid)
		}
	}
	return changed, nil
}
