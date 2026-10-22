package strategy

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/go-git/go-git/v6/plumbing"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// initCountTestRepo initializes an isolated repo with two commits and returns
// the repo dir plus the HEAD (second) commit hash.
func initCountTestRepo(t *testing.T) (dir, head string) {
	t.Helper()
	dir = t.TempDir()
	testutil.InitRepo(t, dir)
	testutil.WriteFile(t, dir, "f.txt", "one")
	testutil.GitAdd(t, dir, "f.txt")
	testutil.GitCommit(t, dir, "first")
	testutil.WriteFile(t, dir, "f.txt", "two")
	testutil.GitAdd(t, dir, "f.txt")
	testutil.GitCommit(t, dir, "second")
	return dir, testutil.GetHeadHash(t, dir)
}

// writeGitRefsBackendSetting writes .entire/settings.json selecting the
// git-refs primary checkpoint backend.
func writeGitRefsBackendSetting(t *testing.T, dir string) {
	t.Helper()
	writeSettingsJSON(t, dir, `{"enabled": true, "checkpoints": {"primary": {"type": "git-refs"}}}`)
}

const v1LocalRef = "refs/heads/" + paths.MetadataBranchName

// Checkpoint IDs for v1 fixtures. Status counts checkpoints, so fixtures write
// some of them more than once to keep commit and checkpoint counts apart.
const (
	countCheckpointA = "a1b2c3d4e5f6"
	countCheckpointB = "0123456789ab"
)

// Not parallel: uses t.Chdir()
func TestCountUnpushedCheckpoints_GitBranch_NoV1Branch(t *testing.T) {
	dir, _ := initCountTestRepo(t)
	t.Chdir(dir)

	got, err := CountUnpushedCheckpoints(context.Background(), "origin")
	require.NoError(t, err)
	assert.Equal(t, 0, got)
}

// Not parallel: uses t.Chdir()
func TestCountUnpushedCheckpoints_GitBranch_TrackingRefEqual(t *testing.T) {
	dir, _ := initCountTestRepo(t)
	tip := testutil.CommitV1Checkpoints(t, dir, "", countCheckpointA)
	testutil.GitUpdateRef(t, dir, v1LocalRef, tip)
	testutil.GitUpdateRef(t, dir, "refs/remotes/origin/"+paths.MetadataBranchName, tip)
	t.Chdir(dir)

	got, err := CountUnpushedCheckpoints(context.Background(), "origin")
	require.NoError(t, err)
	assert.Equal(t, 0, got)
}

// Not parallel: uses t.Chdir()
func TestCountUnpushedCheckpoints_GitBranch_LocalAhead(t *testing.T) {
	dir, _ := initCountTestRepo(t)
	pushed := testutil.CommitV1Checkpoints(t, dir, "", countCheckpointA)
	first := testutil.CommitV1Checkpoints(t, dir, pushed, countCheckpointB)
	tip := testutil.CommitV1Checkpoints(t, dir, first, countCheckpointB)
	testutil.GitUpdateRef(t, dir, v1LocalRef, tip)
	testutil.GitUpdateRef(t, dir, "refs/remotes/origin/"+paths.MetadataBranchName, pushed)
	t.Chdir(dir)

	got, err := CountUnpushedCheckpoints(context.Background(), "origin")
	require.NoError(t, err)
	assert.Equal(t, 1, got, "two v1 commits ahead, both writing one checkpoint")
}

// Not parallel: uses t.Chdir()
func TestCountUnpushedCheckpoints_GitBranch_TrackingRefAbsentCountsAll(t *testing.T) {
	dir, _ := initCountTestRepo(t)
	first := testutil.CommitV1Checkpoints(t, dir, "", countCheckpointA)
	second := testutil.CommitV1Checkpoints(t, dir, first, countCheckpointB)
	tip := testutil.CommitV1Checkpoints(t, dir, second, countCheckpointA)
	testutil.GitUpdateRef(t, dir, v1LocalRef, tip)
	t.Chdir(dir)

	got, err := CountUnpushedCheckpoints(context.Background(), "origin")
	require.NoError(t, err)
	assert.Equal(t, 2, got, "absent tracking ref counts every checkpoint in v1 once, not every commit")
}

// Not parallel: uses t.Chdir()
func TestCountUnpushedCheckpoints_GitRefs_EmptyQueue(t *testing.T) {
	dir, _ := initCountTestRepo(t)
	writeGitRefsBackendSetting(t, dir)
	t.Chdir(dir)

	got, err := CountUnpushedCheckpoints(context.Background(), "origin")
	require.NoError(t, err)
	assert.Equal(t, 0, got)
}

// Not parallel: uses t.Chdir()
func TestCountUnpushedCheckpoints_GitRefs_CountsQueuedRefsThatExist(t *testing.T) {
	dir, second := initCountTestRepo(t)
	writeGitRefsBackendSetting(t, dir)

	// The v1 branch exists but must NOT be counted: the git-refs backend
	// counts the push queue, not v1.
	testutil.GitUpdateRef(t, dir, v1LocalRef, testutil.CommitV1Checkpoints(t, dir, "", countCheckpointA))

	// A queued ref deleted locally is dropped by the push, not sent.
	const kept = "refs/entire/checkpoints/aa/bb0000000001"
	const stale = "refs/entire/checkpoints/aa/bb0000000002"
	testutil.GitUpdateRef(t, dir, kept, second)
	queue := checkpoint.NewPushQueue(filepath.Join(dir, ".git"))
	require.NoError(t, queue.Enqueue(plumbing.ReferenceName(kept)))
	require.NoError(t, queue.Enqueue(plumbing.ReferenceName(stale)))
	t.Chdir(dir)

	got, err := CountUnpushedCheckpoints(context.Background(), "origin")
	require.NoError(t, err)
	assert.Equal(t, 1, got, "git-refs counts queued refs that still exist locally")
}
