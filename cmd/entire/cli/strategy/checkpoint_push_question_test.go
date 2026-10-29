package strategy

import (
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/gitrepo"
	"github.com/entireio/cli/cmd/entire/cli/interactive"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// answerFirstPushQuestionWith makes the first-push question return answer, as
// if typed at the terminal: under test the real question reads nothing.
func answerFirstPushQuestionWith(t *testing.T, answer checkpointPushQuestionAnswer) {
	t.Helper()
	t.Setenv(interactive.EnvTestTTY, "1")
	previous := askCheckpointPushQuestion
	askCheckpointPushQuestion = func() checkpointPushQuestionAnswer { return answer }
	t.Cleanup(func() { askCheckpointPushQuestion = previous })
}

// firstPushWithOuterCheckpointRef runs the git hook's pre-push for a push that
// itself sends a queued checkpoint ref, as `git push --mirror` would.
func firstPushWithOuterCheckpointRef(t *testing.T, answer checkpointPushQuestionAnswer) (bareDir string, err error) {
	t.Helper()
	workDir, bareDir, refs := setupRepoWithCheckpointRefs(t)
	prepareGitRefsPrePush(t, workDir, bareDir)
	answerFirstPushQuestionWith(t, answer)

	repo, openErr := gitrepo.OpenPath(workDir)
	require.NoError(t, openErr)
	defer repo.Close()
	enqueueRefs(t, repo, refs)

	outer := refs[0].String()
	tip := strings.TrimSpace(testutil.RunGit(t, workDir, "rev-parse", outer))
	ctx := WithPrePushRefs(t.Context(), []PrePushRef{{
		LocalRef: outer, LocalSHA: tip, RemoteRef: outer, RemoteSHA: plumbing.ZeroHash.String(),
	}})
	captureStderrWriter(t)
	return bareDir, NewManualCommitStrategy().PrePushFromGitHook(ctx, "origin")
}

// The outer-push check runs before the question, while the policy is still
// unset. Answering manual must still refuse a push that sends checkpoint refs
// itself, or that push uploads what the user just chose to keep.
func TestFirstPushQuestion_ManualRefusesPushCarryingCheckpointRefs(t *testing.T) {
	bareDir, err := firstPushWithOuterCheckpointRef(t, checkpointPushAnswerManual)

	require.ErrorIs(t, err, ErrOuterPushCarriesHeldCheckpoints)
	assert.Contains(t, err.Error(), "--no-verify")
	policy, loadErr := settings.LoadCheckpointPushPolicy(t.Context())
	require.NoError(t, loadErr)
	assert.Equal(t, settings.CheckpointPushPolicyManual, policy, "the answer is saved even though the push is refused")
	assert.Empty(t, strings.TrimSpace(testutil.RunGit(t, bareDir, "for-each-ref", "refs/entire/")), "Entire's own flush sends nothing either")
}

func TestFirstPushQuestion_AutomaticLetsPushCarryingCheckpointRefsThrough(t *testing.T) {
	_, err := firstPushWithOuterCheckpointRef(t, checkpointPushAnswerAutomatic)
	require.NoError(t, err)
}

func TestFirstPushQuestion_ManualWithoutOuterCheckpointRefsHoldsQuietly(t *testing.T) {
	workDir, bareDir, refs := setupRepoWithCheckpointRefs(t)
	prepareGitRefsPrePush(t, workDir, bareDir)
	answerFirstPushQuestionWith(t, checkpointPushAnswerManual)
	repo, err := gitrepo.OpenPath(workDir)
	require.NoError(t, err)
	defer repo.Close()
	enqueueRefs(t, repo, refs)

	stderr := captureStderrWriter(t)
	require.NoError(t, NewManualCommitStrategy().PrePushFromGitHook(WithPrePushRefs(t.Context(), nil), "origin"))

	assert.Contains(t, stderr.String(), "2 checkpoints waiting")
	assertRefsAbsentFromRemote(t, bareDir, refs, "an answer of manual holds this push")
}

// The question can wait a long time. A policy chosen meanwhile, with
// `entire configure` or by a push from another worktree, wins over the answer.
func TestFirstPushQuestion_LateAnswerKeepsNewerChoice(t *testing.T) {
	workDir, bareDir, refs := setupRepoWithCheckpointRefs(t)
	prepareGitRefsPrePush(t, workDir, bareDir)
	t.Setenv(interactive.EnvTestTTY, "1")
	previous := askCheckpointPushQuestion
	askCheckpointPushQuestion = func() checkpointPushQuestionAnswer {
		require.NoError(t, settings.SaveCheckpointPushPolicy(t.Context(), settings.CheckpointPushPolicyManual))
		return checkpointPushAnswerAutomatic
	}
	t.Cleanup(func() { askCheckpointPushQuestion = previous })
	repo, err := gitrepo.OpenPath(workDir)
	require.NoError(t, err)
	defer repo.Close()
	enqueueRefs(t, repo, refs)

	captureStderrWriter(t)
	require.NoError(t, NewManualCommitStrategy().PrePushFromGitHook(WithPrePushRefs(t.Context(), nil), "origin"))

	policy, err := settings.LoadCheckpointPushPolicy(t.Context())
	require.NoError(t, err)
	assert.Equal(t, settings.CheckpointPushPolicyManual, policy)
	assertRefsAbsentFromRemote(t, bareDir, refs, "the newer manual holds this push")
}
