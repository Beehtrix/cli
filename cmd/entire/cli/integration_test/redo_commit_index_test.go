//go:build integration

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/cmd/entire/cli/execx"
	"github.com/entireio/cli/cmd/entire/cli/trailers"
)

// A redo compares the dropped commit with the commit git is preparing. `git
// commit -a` and path-limited commits prepare it in a temporary index, so the
// ordinary index can hold different content than what is being committed.
func TestRedo_ComparesTheCommitIndex(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		rewritten bool
		commit    []string
	}{
		{name: "staged", commit: []string{"commit", "--no-gpg-sign", "-m", "redo"}},
		{name: "all", commit: []string{"commit", "--no-gpg-sign", "-am", "redo"}},
		{name: "partial", commit: []string{"commit", "--no-gpg-sign", "-m", "redo", "--", "a.txt"}},
		{name: "all-rewritten", rewritten: true, commit: []string{"commit", "--no-gpg-sign", "-am", "redo"}},
		{name: "partial-rewritten", rewritten: true, commit: []string{"commit", "--no-gpg-sign", "-m", "redo", "--", "a.txt"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := NewFeatureBranchEnv(t)
			git := gitWithEntireCommitHooks(t, env)
			env.WriteFile("a.txt", "base\n")
			git("add", "a.txt")
			git("commit", "--no-gpg-sign", "-m", "base")
			env.WriteFile("a.txt", "agent\n")
			env.GitAdd("a.txt")
			env.GitCommitWithCheckpointID("agent: edit a", advForeignCheckpoint)

			git("reset", "-q", "HEAD~1")
			want := "agent\n"
			if tc.rewritten {
				git("add", "a.txt")
				want = "human rewrite\n"
				env.WriteFile("a.txt", want)
			}
			if tc.name == "staged" {
				git("add", "a.txt")
			}
			git(tc.commit...)

			require.Equal(t, want, git("show", "HEAD:a.txt"))
			ids := trailers.ParseAllCheckpoints(git("log", "-1", "--format=%B"))
			var got []string
			for _, cpID := range ids {
				got = append(got, cpID.String())
			}
			if tc.rewritten {
				require.NotContains(t, got, advForeignCheckpoint, "rewritten content is new work, not a redo")
				return
			}
			require.Contains(t, got, advForeignCheckpoint, "recommitting the dropped commit's content is a redo")
		})
	}
}

// gitWithEntireCommitHooks runs real git with Entire's commit hooks, so the
// hooks see the environment git gives them, including a temporary index.
func gitWithEntireCommitHooks(t *testing.T, env *TestEnv) func(...string) string {
	t.Helper()
	hooksDir := t.TempDir()
	binary := "'" + strings.ReplaceAll(filepath.ToSlash(getTestBinary()), "'", "'\"'\"'") + "'"
	for _, hook := range []string{"prepare-commit-msg", "post-commit"} {
		script := fmt.Sprintf("#!/bin/sh\nexec %s hooks git %s \"$@\"\n", binary, hook)
		require.NoError(t, os.WriteFile(filepath.Join(hooksDir, hook), []byte(script), 0o755))
	}
	return func(args ...string) string {
		t.Helper()
		args = append([]string{"-c", "core.hooksPath=" + hooksDir}, args...)
		cmd := execx.NonInteractive(t.Context(), "git", args...)
		cmd.Dir = env.RepoDir
		cmd.Env = env.cliEnv()
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
		return string(out)
	}
}
