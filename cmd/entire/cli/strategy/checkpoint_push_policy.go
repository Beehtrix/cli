package strategy

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
)

// checkpointRefSelection says which queued refs one upload may send. The zero
// value selects every queued ref, which is automatic delivery.
type checkpointRefSelection struct {
	// refs, when non-nil, limits the upload to these refs. A non-zero hash
	// pins the commit the caller looked at: the ref is sent only while that
	// commit is still its tip.
	refs map[plumbing.ReferenceName]plumbing.Hash
}

// pin reports whether ref is selected, and the commit it is pinned to.
func (s checkpointRefSelection) pin(ref plumbing.ReferenceName) (plumbing.Hash, bool) {
	if s.refs == nil {
		return plumbing.ZeroHash, true
	}
	hash, ok := s.refs[ref]
	return hash, ok
}

// automaticCheckpointDelivery reports whether paths that upload without being
// told which checkpoints to send may run. An unreadable policy is an error,
// never automatic.
func automaticCheckpointDelivery(repo *git.Repository) (bool, error) {
	commonDir, err := checkpoint.RepositoryCommonDir(repo)
	if err != nil {
		return false, fmt.Errorf("read checkpoint push policy: %w", err)
	}
	policy, err := settings.LoadCheckpointPushPolicyAt(commonDir)
	if err != nil {
		return false, err //nolint:wrapcheck // already names the file and the problem
	}
	return policy == settings.CheckpointPushPolicyAutomatic, nil
}

// CheckpointsHeldForManualPush reports whether new checkpoints wait for
// `entire checkpoint push` instead of uploading now: the git-refs backend under
// any policy but automatic. The policy does not apply to the git-branch
// backend. An unreadable policy is an error naming the file.
func CheckpointsHeldForManualPush(ctx context.Context) (bool, error) {
	if !primaryIsGitRefs(ctx) {
		return false, nil
	}
	repo, err := OpenRepository(ctx)
	if err != nil {
		return false, fmt.Errorf("open repository: %w", err)
	}
	defer repo.Close()
	automatic, err := automaticCheckpointDelivery(repo)
	if err != nil {
		return false, err
	}
	return !automatic, nil
}

// ErrOuterPushCarriesHeldCheckpoints aborts a user push that sends checkpoint
// content itself while the clone's policy holds checkpoints back.
var ErrOuterPushCarriesHeldCheckpoints = errors.New("refusing to push checkpoint content")

// checkOuterPushUnderManualPolicy refuses the user's push when it sends
// checkpoint content itself (`--mirror`, an explicit refspec, the v1 branch)
// and the policy is not automatic or cannot be read. Under manual, checkpoints
// leave only through `entire checkpoint push`, and git has already chosen what
// to send, so the whole push is refused. A deletion sends nothing and passes.
// Without the hook's ref list there is nothing to check; pre-push warns then.
func checkOuterPushUnderManualPolicy(ctx context.Context) error {
	outer, ok := outerPushCheckpointRef(ctx)
	if !ok || !primaryIsGitRefs(ctx) {
		return nil
	}
	name := outer.LocalRef
	if !isCheckpointRefName(name) {
		name = outer.RemoteRef
	}
	const howTo = "Leave checkpoint refs out of this push (avoid --mirror) and upload checkpoints with 'entire checkpoint push'. " +
		"To push them anyway, run git push with --no-verify"

	repo, err := OpenRepository(ctx)
	if err != nil {
		return fmt.Errorf("%w: this push includes %s, and the checkpoint push policy could not be read (open repository: %w). %s",
			ErrOuterPushCarriesHeldCheckpoints, name, err, howTo)
	}
	defer repo.Close()
	automatic, err := automaticCheckpointDelivery(repo)
	if err != nil {
		return fmt.Errorf("%w: this push includes %s, and the checkpoint push policy could not be read (%w). %s",
			ErrOuterPushCarriesHeldCheckpoints, name, err, howTo)
	}
	if automatic {
		return nil
	}
	return fmt.Errorf("%w: this push includes %s, and this clone's checkpoint push policy is manual. %s",
		ErrOuterPushCarriesHeldCheckpoints, name, howTo)
}

// announceCheckpointPushPolicy is the pre-push hook's view of the policy. It
// reports whether automatic delivery may run, and otherwise tells the user why
// their checkpoints stayed behind. It never fails the user's push.
func announceCheckpointPushPolicy(ctx context.Context, repo *git.Repository) bool {
	automatic, err := automaticCheckpointDelivery(repo)
	if err != nil {
		logging.Warn(ctx, "git-refs pre-push: checkpoint push policy unreadable; no checkpoints pushed",
			slog.String("error", err.Error()))
		fmt.Fprintf(stderrWriter, "[entire] No checkpoints were pushed: %v\n", err)
		return false
	}
	if automatic {
		return true
	}
	if waiting := waitingCheckpointCount(ctx, repo); waiting > 0 {
		fmt.Fprintf(stderrWriter, "[entire] %s waiting, upload with: entire checkpoint push\n", pluralCheckpoints(waiting))
		warnPrePushRefsUnknown(ctx, stderrWriter)
	}
	return false
}

// waitingCheckpointCount counts queued refs that exist locally, the same set
// `entire checkpoint list --outgoing` lists. A queue that cannot be read
// counts as zero: the line it feeds is a reminder, not a report.
func waitingCheckpointCount(ctx context.Context, repo *git.Repository) int {
	queued, err := peekCheckpointQueue(ctx, repo)
	if err != nil {
		return 0
	}
	existing, _ := partitionLocalRefs(repo, queued)
	return len(existing)
}

func pluralCheckpoints(n int) string {
	if n == 1 {
		return "1 checkpoint"
	}
	return fmt.Sprintf("%d checkpoints", n)
}

// ErrCheckpointPushDisabled is returned by PushCheckpoints when settings turn
// checkpoint pushing off.
var ErrCheckpointPushDisabled = errors.New("checkpoint pushing is disabled in settings (push_sessions=false)")

// CheckpointPushRequest names one checkpoint ref to upload now. A non-zero
// Commit pins the version the caller looked at.
type CheckpointPushRequest struct {
	Ref    plumbing.ReferenceName
	Commit plumbing.Hash
}

// CheckpointPushStatus is what happened to one CheckpointPushRequest.
type CheckpointPushStatus string

const (
	CheckpointPushUploaded   CheckpointPushStatus = "uploaded"
	CheckpointPushChanged    CheckpointPushStatus = "changed"
	CheckpointPushFailed     CheckpointPushStatus = "failed"
	CheckpointPushNotWaiting CheckpointPushStatus = "not_waiting"
)

// CheckpointPushOutcome reports one target. Commit is the version sent, or
// for a changed target the version that is now waiting instead.
type CheckpointPushOutcome struct {
	Target CheckpointPushRequest
	Commit plumbing.Hash
	Status CheckpointPushStatus
	Err    error
}

// CheckpointPushReport is the result of PushCheckpoints, one outcome per
// target in the order given.
type CheckpointPushReport struct {
	Remote   string
	Outcomes []CheckpointPushOutcome
}

// PushCheckpoints uploads the named queued checkpoint refs to the checkpoint
// sync remote now, through the same delivery as git push, under either
// policy. It does not read the policy: naming the refs is the user's decision,
// and the policy only governs what uploads without being named. It returns
// ErrOutgoingNeedsGitRefs on the git-branch backend.
func PushCheckpoints(ctx context.Context, targets []CheckpointPushRequest) (CheckpointPushReport, error) {
	if !primaryIsGitRefs(ctx) {
		return CheckpointPushReport{}, ErrOutgoingNeedsGitRefs
	}
	syncRemote, err := ResolveCheckpointSyncRemote(ctx)
	if err != nil {
		return CheckpointPushReport{}, err
	}
	if syncRemote.Name == "" {
		return CheckpointPushReport{}, errors.New("no git remote to push checkpoints to")
	}
	ps := resolvePushSettings(ctx, syncRemote.Name)
	if ps.pushDisabled {
		return CheckpointPushReport{}, ErrCheckpointPushDisabled
	}
	// OPF reads redaction config that only this sets; see opfGateForCheckpointRefs.
	if err := EnsureRedactionConfigured(ctx); err != nil {
		return CheckpointPushReport{}, fmt.Errorf("configure redaction before pushing: %w", err)
	}

	repo, err := OpenRepository(ctx)
	if err != nil {
		return CheckpointPushReport{}, fmt.Errorf("open repository: %w", err)
	}
	defer repo.Close()

	selection := checkpointRefSelection{refs: make(map[plumbing.ReferenceName]plumbing.Hash, len(targets))}
	for _, target := range targets {
		if pinned, ok := selection.refs[target.Ref]; ok && pinned != target.Commit {
			return CheckpointPushReport{}, fmt.Errorf("%s is named with two different commits; name each checkpoint once", target.Ref)
		}
		selection.refs[target.Ref] = target.Commit
	}
	waiting, err := waitingCheckpointRefs(ctx, repo)
	if err != nil {
		return CheckpointPushReport{}, err
	}

	report := CheckpointPushReport{Remote: syncRemote.Name}
	if anyTargetWaiting(targets, waiting) {
		if opfErr := opfGateForCheckpointRefs(ctx, repo); opfErr != nil {
			return report, fmt.Errorf("checkpoint refs stay queued: %w", opfErr)
		}
	}
	result, flushErr := flushCheckpointRefs(ctx, repo, ps, selection)
	report.Outcomes = checkpointPushOutcomes(targets, waiting, result, flushErr)
	if flushErr != nil {
		logging.Warn(ctx, "checkpoint push: delivery failed",
			slog.String("error", flushErr.Error()))
	}
	return report, nil
}

// waitingCheckpointRefs is the set of queued refs present locally.
func waitingCheckpointRefs(ctx context.Context, repo *git.Repository) (map[plumbing.ReferenceName]struct{}, error) {
	queued, err := peekCheckpointQueue(ctx, repo)
	if err != nil {
		return nil, err
	}
	existing, _ := partitionLocalRefs(repo, queued)
	set := make(map[plumbing.ReferenceName]struct{}, len(existing))
	for _, ref := range existing {
		set[ref] = struct{}{}
	}
	return set, nil
}

func anyTargetWaiting(targets []CheckpointPushRequest, waiting map[plumbing.ReferenceName]struct{}) bool {
	for _, target := range targets {
		if _, ok := waiting[target.Ref]; ok {
			return true
		}
	}
	return false
}

// checkpointPushOutcomes maps the flush back onto each target. A flush-wide
// error (one with no per-ref report, such as a failed queue drain) fails every
// waiting target the flush did not report on, rather than reading as absence.
func checkpointPushOutcomes(
	targets []CheckpointPushRequest,
	waiting map[plumbing.ReferenceName]struct{},
	result checkpointFlushResult,
	flushErr error,
) []CheckpointPushOutcome {
	pushed := make(map[plumbing.ReferenceName]plumbing.Hash, len(result.pushed))
	for _, ref := range result.pushed {
		pushed[ref.name] = ref.hash
	}
	changed := make(map[plumbing.ReferenceName]plumbing.Hash, len(result.changed))
	for _, ref := range result.changed {
		changed[ref.name] = ref.hash
	}

	outcomes := make([]CheckpointPushOutcome, 0, len(targets))
	for _, target := range targets {
		outcome := CheckpointPushOutcome{Target: target}
		if hash, ok := pushed[target.Ref]; ok {
			outcome.Status, outcome.Commit = CheckpointPushUploaded, hash
		} else if hash, ok := changed[target.Ref]; ok {
			outcome.Status, outcome.Commit = CheckpointPushChanged, hash
		} else if err, ok := result.failed[target.Ref]; ok {
			outcome.Status, outcome.Err = CheckpointPushFailed, err
			if errors.Is(err, errCheckpointRefSuperseded) {
				outcome.Status = CheckpointPushChanged
			}
		} else if _, queued := waiting[target.Ref]; queued && flushErr != nil {
			outcome.Status, outcome.Err = CheckpointPushFailed, flushErr
		} else {
			// Not queued, or it left the queue while this command ran.
			outcome.Status = CheckpointPushNotWaiting
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes
}
