package gitrepo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// HiddenFromStatus reports whether a `git ls-files -v` tag marks a path
// skip-worktree ("S", or "s" with assume-unchanged too) or assume-unchanged
// (a lowercase tag): paths whose worktree changes git status does not report.
func HiddenFromStatus(tag string) bool {
	return tag == "S" || tag != strings.ToUpper(tag)
}

// HiddenWorktreeChanges returns the tracked paths marked assume-unchanged or
// skip-worktree whose worktree entry differs from the index: edited, deleted,
// or replaced by another kind of entry (a file by a symlink, or the reverse).
// git status does not report them. A skip-worktree path missing from the
// worktree is a sparse checkout's omission and is not returned. Submodules and
// unmerged entries are left out.
//
// The entries are copied, without those flags, into a temporary index, which
// is refreshed and read with `git diff-files`. git therefore compares them by
// its own rules: clean filters and the index-aware line-ending conversion
// apply as they do for git status (see PathStatuses), which hashing the files
// would not do. The repository's own index is only read.
//
// This is for commands the user runs: unlike the package's hook-path reads,
// it keeps the repository selectors the caller exported (GIT_DIR,
// GIT_WORK_TREE, GIT_INDEX_FILE), so it reads the same index git status does
// in that environment.
func HiddenWorktreeChanges(ctx context.Context, worktreeRoot string) ([]string, error) {
	out, err := callerGitCommand(ctx, worktreeRoot, nil, "ls-files", "-z", "-v", "--stage").Output()
	if err != nil {
		return nil, gitCommandError("ls-files", err)
	}
	var entries strings.Builder
	sparse := make(map[string]bool)
	for _, record := range strings.Split(string(out), "\x00") {
		// "<tag> <mode> <object> <stage>\t<path>"
		tag, entry, _ := strings.Cut(record, " ")
		meta, path, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || fields[2] != "0" || !HiddenFromStatus(tag) || !worktreeEntryMode(fields[0]) {
			continue
		}
		sparse[path] = strings.EqualFold(tag, "S")
		entries.WriteString(entry)
		entries.WriteByte(0)
	}
	if len(sparse) == 0 {
		return nil, nil
	}
	changes, err := diffFilesWithoutFlags(ctx, worktreeRoot, entries.String())
	if err != nil {
		return nil, err
	}
	var changed []string
	for path, status := range changes {
		if status != "D" || !sparse[path] {
			changed = append(changed, path)
		}
	}
	return changed, nil
}

// callerGitCommand is worktreeGitCommand with the caller's own environment,
// selectors included, plus env, whose entries win over the inherited ones.
func callerGitCommand(ctx context.Context, worktreeRoot string, env []string, args ...string) *exec.Cmd {
	cmd := worktreeGitCommand(ctx, worktreeRoot, args...)
	cmd.Env = append(os.Environ(), env...)
	return cmd
}

// worktreeEntryMode reports whether an index mode is a regular file or a
// symlink, the entries this repository's worktree holds (not a gitlink).
func worktreeEntryMode(mode string) bool {
	return mode == "100644" || mode == "100755" || mode == "120000"
}

// diffFilesWithoutFlags writes entries (NUL-terminated `git ls-files --stage`
// records) into a temporary index and returns git diff-files' status for each
// path whose worktree entry differs.
func diffFilesWithoutFlags(ctx context.Context, worktreeRoot, entries string) (map[string]string, error) {
	dir, err := os.MkdirTemp("", "entire-hidden-index-")
	if err != nil {
		return nil, fmt.Errorf("create a temporary index: %w", err)
	}
	defer os.RemoveAll(dir)
	index, err := filepath.Abs(filepath.Join(dir, "index"))
	if err != nil {
		return nil, fmt.Errorf("create a temporary index: %w", err)
	}
	git := func(stdin string, args ...string) ([]byte, error) {
		// core.ignoreStat would mark every imported entry assume-unchanged
		// again, a split index would leave a shared index file in the
		// repository, and fsmonitor could start its daemon for a throwaway
		// index.
		cmd := callerGitCommand(ctx, worktreeRoot, []string{"GIT_INDEX_FILE=" + index},
			append([]string{"-c", "core.ignoreStat=false", "-c", "core.splitIndex=false", "-c", "core.fsmonitor=false"}, args...)...)
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.Output()
		if err != nil {
			return nil, gitCommandError(args[0], err)
		}
		return out, nil
	}
	if _, err := git(entries, "update-index", "-z", "--index-info"); err != nil {
		return nil, err
	}
	// The copied entries carry no stat data, so the refresh compares each
	// one's contents and records the stat of those that match.
	if _, err := git("", "update-index", "-q", "--refresh"); err != nil {
		return nil, err
	}
	out, err := git("", "diff-files", "-z", "--name-status")
	if err != nil {
		return nil, err
	}
	// "<status>\0<path>\0" per entry.
	records := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	changes := make(map[string]string, len(records)/2)
	for i := 0; i+1 < len(records); i += 2 {
		changes[records[i+1]] = records[i]
	}
	return changes, nil
}

// gitCommandError wraps a failed `git <name>` run with its stderr, which
// exec.Cmd.Output keeps on the exit error.
func gitCommandError(name string, err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(bytes.TrimSpace(exitErr.Stderr)) > 0 {
		return fmt.Errorf("git %s: %w: %s", name, err, bytes.TrimSpace(exitErr.Stderr))
	}
	return fmt.Errorf("git %s: %w", name, err)
}
