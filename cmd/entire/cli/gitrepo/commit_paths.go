package gitrepo

import (
	"context"
	"fmt"
	"io"
)

// CommitPaths stages paths (relative to repoRoot) and commits only them with
// message (`git add` then `git commit --only`), so anything else already
// staged stays staged and out of the commit. It runs git in the environment
// PathStatuses uses — repository selectors and pathspec magic stripped,
// literal pathspecs — so it commits the files PathStatuses reported. The
// repository's own hooks and commit signing apply, and git's output goes to
// out. Unlike the classification helpers it has no budget of its own: a
// pre-commit hook may legitimately take a while, so only ctx bounds it.
func CommitPaths(ctx context.Context, repoRoot string, paths []string, message string, out io.Writer) error {
	steps := [][]string{
		{"add"},
		{"commit", "-q", "--only", "-m", message},
	}
	for _, args := range steps {
		cmd := literalPathspecCommand(ctx, repoRoot, paths, args...)
		cmd.Stdout = out
		cmd.Stderr = out
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("git %s: %w", args[0], err)
		}
	}
	return nil
}
