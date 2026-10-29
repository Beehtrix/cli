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
		fmt.Fprintf(stderrWriter, "[entire] %s waiting, upload with: entire checkpoint push --all\n", pluralCheckpoints(waiting))
	}
	return false
}

// waitingCheckpointCount counts queued refs that exist locally, the same set
// `entire checkpoint list --outgoing` lists. A queue that cannot be read
// counts as zero: the line it feeds is a reminder, not a report.
func waitingCheckpointCount(ctx context.Context, repo *git.Repository) int {
	waiting, err := waitingCheckpointRefs(ctx, repo)
	if err != nil {
		return 0
	}
	return len(waiting)
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
	// CheckpointPushNotFound is a named checkpoint with no ref here at all,
	// such as a mistyped ID.
	CheckpointPushNotFound CheckpointPushStatus = "not_found"
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
	isRefs, err := primaryBackendIsGitRefs(ctx)
	if err != nil {
		return CheckpointPushReport{}, err
	}
	if !isRefs {
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

	waiting, err := waitingCheckpointRefs(ctx, repo)
	if err != nil {
		return CheckpointPushReport{}, err
	}
	selection, err := waitingTargetSelection(targets, waiting)
	if err != nil {
		return CheckpointPushReport{}, err
	}

	missing := missingCheckpointRefs(repo, targets, waiting)
	report := CheckpointPushReport{Remote: syncRemote.Name}
	if len(selection.refs) == 0 {
		report.Outcomes = checkpointPushOutcomes(targets, waiting, missing, checkpointFlushResult{}, nil)
		return report, nil
	}
	if opfErr := opfGateForCheckpointRefs(ctx, repo); opfErr != nil {
		return report, fmt.Errorf("checkpoint refs stay queued: %w", opfErr)
	}
	result, flushErr := flushCheckpointRefs(ctx, repo, ps, selection)
	report.Outcomes = checkpointPushOutcomes(targets, waiting, missing, result, flushErr)
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

// waitingTargetSelection selects the named refs that were waiting when the
// queue was read. The OPF gate runs only for those, so a named ref queued
// after that read is left for the next upload rather than sent unscanned.
func waitingTargetSelection(targets []CheckpointPushRequest, waiting map[plumbing.ReferenceName]struct{}) (checkpointRefSelection, error) {
	named := make(map[plumbing.ReferenceName]plumbing.Hash, len(targets))
	for _, target := range targets {
		if pinned, ok := named[target.Ref]; ok && pinned != target.Commit {
			return checkpointRefSelection{}, fmt.Errorf("%s is named with two different commits; name each checkpoint once", target.Ref)
		}
		named[target.Ref] = target.Commit
	}
	selection := checkpointRefSelection{refs: make(map[plumbing.ReferenceName]plumbing.Hash, len(named))}
	for ref, commit := range named {
		if _, ok := waiting[ref]; ok {
			selection.refs[ref] = commit
		}
	}
	return selection, nil
}

// checkpointPushOutcomes maps the flush back onto each target. A flush-wide
// error (one with no per-ref report, such as a failed queue drain) fails every
// waiting target the flush did not report on, rather than reading as absence.
// missingCheckpointRefs is the named refs that are not waiting and do not
// exist here, as opposed to ones already uploaded.
func missingCheckpointRefs(repo *git.Repository, targets []CheckpointPushRequest, waiting map[plumbing.ReferenceName]struct{}) map[plumbing.ReferenceName]struct{} {
	missing := make(map[plumbing.ReferenceName]struct{})
	for _, target := range targets {
		if _, ok := waiting[target.Ref]; ok {
			continue
		}
		if _, err := repo.Reference(target.Ref, true); errors.Is(err, plumbing.ErrReferenceNotFound) {
			missing[target.Ref] = struct{}{}
		}
	}
	return missing
}

func checkpointPushOutcomes(
	targets []CheckpointPushRequest,
	waiting map[plumbing.ReferenceName]struct{},
	missing map[plumbing.ReferenceName]struct{},
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
		} else if _, absent := missing[target.Ref]; absent {
			outcome.Status = CheckpointPushNotFound
		} else {
			// Not queued, or it left the queue while this command ran.
			outcome.Status = CheckpointPushNotWaiting
		}
		outcomes = append(outcomes, outcome)
	}
	return outcomes
}
