package strategy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/cmd/entire/cli/gitrepo"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

func TestOpenSessionCondensationRepository_ReusedRegisteredPath(t *testing.T) { //nolint:paralleltest // uses t.Chdir
	mainDir, linkedDir := twoWorktrees(t)
	t.Chdir(mainDir)
	require.NoError(t, os.RemoveAll(linkedDir))
	testutil.InitRepo(t, linkedDir)
	registered, err := gitrepo.ListWorktreePaths(t.Context(), mainDir)
	require.NoError(t, err)
	require.Contains(t, registered, linkedDir, "fixture must retain the stale worktree registration")

	repo, err := openSessionCondensationRepository(t.Context(), &SessionState{WorktreePath: linkedDir})
	require.NoError(t, err)
	defer repo.Close()
	worktree, err := repo.Worktree()
	require.NoError(t, err)
	assert.Equal(t, mainDir, worktree.Filesystem().Root(), "a replacement repository must not receive the session's checkpoint")
}

func TestOpenSessionCondensationRepository(t *testing.T) { //nolint:paralleltest // uses t.Chdir
	mainDir, linkedDir := twoWorktrees(t)
	t.Chdir(mainDir)
	unrelated := resolvedTempDir(t)
	testutil.InitRepo(t, unrelated)

	for _, tc := range []struct {
		name     string
		recorded string
		want     string
	}{
		{name: "linked home", recorded: linkedDir, want: linkedDir},
		{name: "current home", recorded: mainDir, want: mainDir},
		{name: "no recorded home", want: mainDir},
		{name: "relative path", recorded: filepath.Base(linkedDir), want: mainDir},
		{name: "unrelated repository", recorded: unrelated, want: mainDir},
	} {
		t.Run(tc.name, func(t *testing.T) { //nolint:paralleltest // parent uses t.Chdir
			repo, err := openSessionCondensationRepository(t.Context(), &SessionState{WorktreePath: tc.recorded})
			require.NoError(t, err)
			defer repo.Close()
			worktree, err := repo.Worktree()
			require.NoError(t, err)
			assert.Equal(t, tc.want, worktree.Filesystem().Root())
		})
	}

	t.Run("removed but still registered", func(t *testing.T) { //nolint:paralleltest // parent uses t.Chdir
		require.NoError(t, os.RemoveAll(linkedDir))
		repo, err := openSessionCondensationRepository(t.Context(), &SessionState{WorktreePath: linkedDir})
		require.NoError(t, err)
		defer repo.Close()
		worktree, err := repo.Worktree()
		require.NoError(t, err)
		assert.Equal(t, mainDir, worktree.Filesystem().Root())
	})
}
