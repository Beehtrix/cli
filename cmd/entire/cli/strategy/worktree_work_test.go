package strategy

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/session"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

// Uses t.Chdir — do NOT add t.Parallel().

// twoWorktrees returns a repository and a linked worktree beside it.
func twoWorktrees(t *testing.T) (mainDir, linkedDir string) {
	t.Helper()
	testutil.IsolateGitConfigEnv(t)
	mainDir = setupSessionMatchRepo(t)
	linkedDir = filepath.Join(resolvedTempDir(t), "linked")
	createSessionMatchWorktree(t, mainDir, linkedDir, "linked")
	t.Cleanup(func() { removeSessionMatchWorktree(mainDir, linkedDir) })
	return mainDir, linkedDir
}

func linkedKey(t *testing.T, dir string) string {
	t.Helper()
	worktreeID, err := paths.GetWorktreeID(dir)
	require.NoError(t, err)
	return session.WorktreeKey(worktreeID)
}

func TestEnterWorktreeView(t *testing.T) {
	mainDir, linkedDir := twoWorktrees(t)
	key := linkedKey(t, linkedDir)
	state := &SessionState{
		SessionID:         "view",
		WorktreePath:      mainDir,
		BaseCommit:        "home-base",
		FilesTouched:      []string{"home.go"},
		TouchedFileHashes: map[string]string{"home.go": "h"},
		OtherWorktrees: map[string]*session.WorktreeWork{
			key: {Path: linkedDir, BaseCommit: "linked-base", FilesTouched: []string{"a.go", "b.go"}, TouchedFileHashes: map[string]string{"a.go": "ha"}},
		},
	}

	_, ok := enterWorktreeView(state, mainDir)
	assert.False(t, ok, "the home has no view of its own")

	leave, ok := enterWorktreeView(state, linkedDir)
	require.True(t, ok)
	assert.Equal(t, []string{"a.go", "b.go"}, state.FilesTouched)
	assert.Equal(t, "linked-base", state.BaseCommit)
	assert.Equal(t, linkedDir, state.WorktreePath)
	assert.True(t, state.HasPendingFiles())
	assert.Equal(t, 3, state.PendingFileCount(), "the home's files still count while in the view")

	// A commit in the linked worktree took a.go.
	state.FilesTouched = []string{"b.go"}
	state.BaseCommit = "linked-new"
	leave()
	assert.Equal(t, []string{"home.go"}, state.FilesTouched)
	assert.Equal(t, "home-base", state.BaseCommit)
	assert.Equal(t, mainDir, state.WorktreePath)
	assert.Equal(t, []string{"b.go"}, state.OtherWorktrees[key].FilesTouched)
	assert.Equal(t, "linked-new", state.OtherWorktrees[key].BaseCommit)
	assert.Len(t, state.OtherWorktrees, 1)

	// Once that worktree has nothing left, its entry goes.
	leave, ok = enterWorktreeView(state, linkedDir)
	require.True(t, ok)
	state.FilesTouched = nil
	leave()
	assert.Nil(t, state.OtherWorktrees)
}

// homedWithEntry saves a session homed in mainDir that recorded a.go in
// linkedDir, plus a session homed in linkedDir with nothing pending, so the
// linked worktree's own sessions are an exact match there.
func homedWithEntry(t *testing.T, mainDir, linkedDir string) {
	t.Helper()
	ctx := context.Background()
	s := &ManualCommitStrategy{}
	saveSessionMatchState(ctx, t, s, linkedDir, &SessionState{SessionID: "linked-own", WorktreePath: linkedDir})
	require.NoError(t, MutateSessionState(ctx, "linked-own", func(st *SessionState) error {
		st.Phase = session.PhaseIdle
		return nil
	}))
	testutil.WriteFile(t, linkedDir, "a.go", "package a\n")
	hashes := hashTouchedFiles(ctx, linkedDir, []string{"a.go"})
	saveSessionMatchState(ctx, t, s, mainDir, &SessionState{
		SessionID:    "from-main",
		WorktreePath: mainDir,
		FilesTouched: []string{"home.go"},
		StepCount:    1,
		OtherWorktrees: map[string]*session.WorktreeWork{
			linkedKey(t, linkedDir): {Path: linkedDir, FilesTouched: []string{"a.go"}, TouchedFileHashes: hashes},
		},
	})
	require.NoError(t, MutateSessionState(ctx, "from-main", func(st *SessionState) error {
		st.Phase = session.PhaseIdle
		return nil
	}))
}

// A session that recorded edits in a worktree joins that worktree's linking
// set — marked, and seen through its record of that worktree — even when the
// worktree has sessions of its own.
func TestCommitLinking_IncludesSessionsThatEditedTheWorktree(t *testing.T) {
	mainDir, linkedDir := twoWorktrees(t)
	homedWithEntry(t, mainDir, linkedDir)

	t.Chdir(linkedDir)
	clearSessionMatchCaches()
	sessions, err := (&ManualCommitStrategy{}).findSessionsForCommitLinking(context.Background(), linkedDir)
	require.NoError(t, err)
	var fromMain *SessionState
	for _, st := range sessions {
		if st.SessionID == "from-main" {
			fromMain = st
		}
	}
	require.NotNil(t, fromMain, "the session that edited this worktree is not in its linking set")
	assert.True(t, fromMain.LinkedByWorktreeEntry)
	assert.Equal(t, []string{"a.go"}, fromMain.FilesTouched, "it should be judged by what it recorded here")
}

// Linked only by its record of a worktree, a session stamps a commit there
// only when the commit holds those files.
func TestPrepareCommitMsg_SessionFromAnotherWorktreeNeedsItsFiles(t *testing.T) {
	mainDir, linkedDir := twoWorktrees(t)
	homedWithEntry(t, mainDir, linkedDir)

	t.Chdir(linkedDir)
	clearSessionMatchCaches()
	commit := func(file string) string {
		t.Helper()
		msg := filepath.Join(t.TempDir(), "COMMIT_EDITMSG")
		require.NoError(t, os.WriteFile(msg, []byte("commit "+file+"\n"), 0o600))
		testutil.GitAdd(t, linkedDir, file)
		require.NoError(t, (&ManualCommitStrategy{}).PrepareCommitMsg(context.Background(), msg, "message"))
		data, err := os.ReadFile(msg)
		require.NoError(t, err)
		testutil.RunGit(t, linkedDir, "reset", "-q")
		return string(data)
	}

	testutil.WriteFile(t, linkedDir, "other.go", "package other\n")
	assert.NotContains(t, commit("other.go"), "Entire-Checkpoint", "a commit without the session's files in this worktree must not link it")
	assert.Contains(t, commit("a.go"), "Entire-Checkpoint", "a commit of the session's file in this worktree should link it")
}
