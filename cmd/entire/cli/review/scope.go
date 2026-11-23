// Package review — see env.go for package-level rationale.
//
// scope.go implements scope detection for `entire review`. The scope is the
// git ref the review is bounded by: "commits unique to this branch vs
// <baseRef>". Pinning the scope at launch time prevents the divergent-default
// problem where different agents default to different comparison points (e.g.
// codex used origin/main...HEAD, claude used working-tree-only) on the same
// invocation.
package review

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/gitexec"
	"github.com/entireio/cli/cmd/entire/cli/gitrepo"
	"github.com/entireio/cli/cmd/entire/cli/worktreedir"
	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
)

// ScopeStats summarises the scope of a review for the banner.
type ScopeStats struct {
	BaseRef       string
	CurrentBranch string // empty for detached HEAD
	Commits       int    // commits unique to current branch vs BaseRef
	FilesChanged  int    // files changed across those commits
	Uncommitted   int    // uncommitted file changes in the working tree
}

// formatScopeBanner produces the line printed before agent launch:
//
//	"Reviewing feat/X vs main: 3 commits, 7 files changed, 2 uncommitted"
//
// Pluralisation: "1 commit" / "2 commits", "1 file" / "N files",
// always "uncommitted" (a count, not a noun phrase).
func formatScopeBanner(stats ScopeStats) string {
	subject := stats.CurrentBranch
	if subject == "" {
		subject = "detached HEAD"
	}

	commitWord := "commits"
	if stats.Commits == 1 {
		commitWord = "commit"
	}
	fileWord := "files"
	if stats.FilesChanged == 1 {
		fileWord = "file"
	}

	return fmt.Sprintf(
		"Reviewing %s vs %s: %d %s, %d %s changed, %d uncommitted",
		subject,
		displayRef(stats.BaseRef),
		stats.Commits,
		commitWord,
		stats.FilesChanged,
		fileWord,
		stats.Uncommitted,
	)
}

// ComputeScopeStats gathers the data formatScopeBanner needs.
// Used to build the banner before launching agents.
//
// baseOverride, if non-empty, bypasses the mainline auto-detection and is
// used as the scope base directly. This is the entry point for the
// `--base <ref>` command-line flag. The override is verified via
// `repo.ResolveRevision(plumbing.Revision(<ref>))` (matching the codebase
// pattern in explain.go:156) before being used; an unknown ref produces an
// error before any agents are spawned, so users learn about the typo
// immediately instead of after a 10-minute review run scoped against a
// default the flag was supposed to override.
func ComputeScopeStats(ctx context.Context, repo *git.Repository, baseOverride string) (ScopeStats, error) {
	repoRoot, err := repoWorktreePath(repo)
	if err != nil {
		return ScopeStats{}, fmt.Errorf("get repo root: %w", err)
	}

	var baseRef string
	if baseOverride != "" {
		// Validate via go-git rather than shelling out — matches the codebase
		// pattern in explain.go:156 (resolveCommitUnambiguous). ResolveRevision
		// handles branches, tags, abbreviated SHAs, and HEAD-relative refs, and
		// dereferences annotated tags to their target commit automatically.
		if _, vErr := repo.ResolveRevision(plumbing.Revision(baseOverride)); vErr != nil {
			return ScopeStats{}, fmt.Errorf("base ref %q does not resolve to a commit: %w", baseOverride, vErr)
		}
		baseRef = baseOverride
	} else {
		baseRef, err = fallbackScopeRef(repo)
		if err != nil {
			return ScopeStats{}, fmt.Errorf("detect scope base ref: %w", err)
		}
	}

	currentBranch := currentBranchName(repo)

	commits, err := countCommits(ctx, repoRoot, baseRef)
	if err != nil {
		return ScopeStats{}, fmt.Errorf("count commits: %w", err)
	}

	filesChanged, err := countFilesChanged(ctx, repoRoot, baseRef)
	if err != nil {
		return ScopeStats{}, fmt.Errorf("count files changed: %w", err)
	}

	uncommitted, err := countUncommitted(ctx, repoRoot)
	if err != nil {
		return ScopeStats{}, fmt.Errorf("count uncommitted: %w", err)
	}

	return ScopeStats{
		BaseRef:       baseRef,
		CurrentBranch: currentBranch,
		Commits:       commits,
		FilesChanged:  filesChanged,
		Uncommitted:   uncommitted,
	}, nil
}

// repoWorktreePath returns the working-tree path for repo, or an error if the
// repo is bare or its worktree can't be resolved. ComputeScopeStats uses this
// as the cwd for the runGit invocations in countCommits / countFilesChanged /
// countUncommitted.
func repoWorktreePath(repo *git.Repository) (string, error) {
	wt, err := repo.Worktree()
	if err != nil {
		return "", fmt.Errorf("resolve worktree: %w", err)
	}
	return wt.Filesystem().Root(), nil
}

// fallbackScopeRef returns the mainline ref the review should be scoped
// against: the first existing ref from the fallback chain origin/HEAD →
// origin/main → origin/master → main → master. Returns an error naming the
// tried refs if none exist.
//
// A previous implementation tried to be clever: it picked the merged-into-HEAD
// branch with the most recent committerdate, on the theory that stacked PRs
// (feature B branched off feature A while A is still open) would benefit
// from reviewing against the immediate parent rather than mainline. In
// practice the heuristic routinely picked unrelated recently-merged feature
// branches — `git fetch` mirrors all of origin's branches by default, and
// any branch whose tip is newer than mainline AND merged into mainline
// (i.e., every recently-merged PR branch not yet deleted on origin) was a
// candidate. Reviews ended up scoped against random PR branches, dragging
// in 30+ commits of unrelated upstream work and producing reviews with
// nothing to do with the current branch.
//
// Stacked PR review is now served by the explicit `--base <ref>` flag at
// the command surface, not an inference. The default stays predictable
// (always mainline); the override is explicit when users actually want it.
//
// The result is a full ref name. A short name like "origin/HEAD" also matches
// a tag or local branch of that name, and git resolves the tag first; a tag
// fetched along with someone's commits could then make their branch look like
// the default branch to the trust gate.
func fallbackScopeRef(repo *git.Repository) (string, error) {
	const originHEAD = plumbing.ReferenceName("refs/remotes/origin/HEAD")
	if ref, err := repo.Reference(originHEAD, false); err == nil {
		if ref.Type() != plumbing.SymbolicReference {
			return originHEAD.String(), nil
		}
		if target := ref.Target(); target.IsRemote() && refExists(repo, target) {
			return target.String(), nil
		}
	}
	chain := []plumbing.ReferenceName{"refs/remotes/origin/main", "refs/remotes/origin/master", "refs/heads/main", "refs/heads/master"}
	for _, name := range chain {
		if refExists(repo, name) {
			return name.String(), nil
		}
	}
	return "", errors.New("no mainline ref found (tried origin/HEAD, origin/main, origin/master, main, master); " +
		"pass --base <ref> to scope the review explicitly, or run `git fetch` to populate origin refs")
}

// refExists reports whether the full ref name exists in repo.
func refExists(repo *git.Repository, name plumbing.ReferenceName) bool {
	_, err := repo.Reference(name, true)
	return err == nil
}

// displayRef shortens a full ref name for the scope banner.
func displayRef(ref string) string {
	if strings.HasPrefix(ref, "refs/") {
		return plumbing.ReferenceName(ref).Short()
	}
	return ref
}

// currentBranchName returns the short branch name for the current HEAD,
// or "" for detached HEAD.
func currentBranchName(repo *git.Repository) string {
	head, err := repo.Head()
	if err != nil {
		return ""
	}
	if !head.Name().IsBranch() {
		return "" // detached HEAD
	}
	return head.Name().Short()
}

// countCommits returns the number of commits in <baseRef>..HEAD
// (commits on the current branch not in baseRef).
func countCommits(ctx context.Context, repoRoot, baseRef string) (int, error) {
	out, err := runGit(ctx, repoRoot, "rev-list", "--count", baseRef+"..HEAD")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, fmt.Errorf("parse commit count: %w", err)
	}
	return n, nil
}

// countFilesChanged returns the number of unique files changed on this
// branch since it diverged from baseRef. Uses three-dot diff syntax
// (`git diff base...HEAD`, equivalent to `git diff $(merge-base) HEAD`) so
// upstream-only changes on baseRef after the branch point are NOT counted
// as reversed deltas. Two-dot (`base..HEAD`) would over-count: every file
// modified on mainline since the branch was cut would appear as a
// "removed" change in the diff, inflating the banner's file count.
func countFilesChanged(ctx context.Context, repoRoot, baseRef string) (int, error) {
	out, err := runGit(ctx, repoRoot, "diff", "--name-only", baseRef+"...HEAD")
	if err != nil {
		return 0, err
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return 0, nil
	}
	return len(strings.Split(trimmed, "\n")), nil
}

// listFilesChanged returns the repo-relative paths HEAD adds or modifies
// since its merge base with baseRef. Deleted paths are left out.
func listFilesChanged(ctx context.Context, repoRoot, baseRef string) ([]string, error) {
	out, err := runGit(ctx, repoRoot, "diff", "--name-only", "-z", "--diff-filter=d", baseRef+"...HEAD")
	if err != nil {
		return nil, fmt.Errorf("git diff --name-only: %w", err)
	}
	return splitNUL(out), nil
}

// listUncommitted returns the repo-relative paths of every staged, unstaged,
// and untracked (but not ignored) file, listing files inside untracked
// directories individually, and separately the paths the uncommitted changes
// remove: deletions and the origins of renames.
func listUncommitted(ctx context.Context, repoRoot string) (changed, removed []string, err error) {
	out, err := runGit(ctx, repoRoot, "--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, nil, fmt.Errorf("git status: %w", err)
	}
	codes := make(map[string]string)
	removed = gitrepo.ParsePorcelainV1Z([]byte(out), codes)
	for file, code := range codes {
		if strings.Contains(code, "D") {
			removed = append(removed, file)
		} else {
			changed = append(changed, file)
		}
	}
	return changed, removed, nil
}

// listTracked returns every path in the index, and separately those marked
// skip-worktree or assume-unchanged, whose worktree text git hides from the
// change.
func listTracked(ctx context.Context, repoRoot string) (tracked, hidden map[string]struct{}, err error) {
	out, err := runGit(ctx, repoRoot, "ls-files", "-z", "-v", "--cached")
	if err != nil {
		return nil, nil, fmt.Errorf("git ls-files: %w", err)
	}
	tracked = make(map[string]struct{})
	hidden = make(map[string]struct{})
	for _, record := range splitNUL(out) {
		tag, file, ok := strings.Cut(record, " ")
		if !ok {
			continue
		}
		tracked[file] = struct{}{}
		if hiddenIndexTag(tag) {
			hidden[file] = struct{}{}
		}
	}
	return tracked, hidden, nil
}

// hiddenIndexTag reports whether a `git ls-files -v` tag marks a path
// skip-worktree ("S") or assume-unchanged (lowercase).
func hiddenIndexTag(tag string) bool {
	return tag == "S" || tag != strings.ToUpper(tag)
}

// countWorktreeChanges returns how many paths differ between the worktree and
// HEAD: staged, unstaged and untracked files, whatever
// status.showUntrackedFiles says, plus the files marked assume-unchanged or
// skip-worktree that git status does not report, which are also counted in
// hidden.
func countWorktreeChanges(ctx context.Context, repoRoot string) (paths, hidden int, err error) {
	changed, removed, err := listUncommitted(ctx, repoRoot)
	if err != nil {
		return 0, 0, err
	}
	edited, err := listHiddenEdits(ctx, repoRoot)
	if err != nil {
		return 0, 0, err
	}
	seen := make(map[string]bool)
	for _, file := range slices.Concat(changed, removed) {
		seen[file] = true
	}
	for _, file := range edited {
		if !seen[file] {
			seen[file] = true
			hidden++
		}
	}
	return len(seen), hidden, nil
}

// listHiddenEdits returns the tracked files marked skip-worktree or
// assume-unchanged whose worktree copy differs from the index: edited,
// deleted, or replaced by another kind of entry. git status does not report
// them, but a reviewer reads the worktree. A skip-worktree path missing from
// the worktree is a sparse checkout's omission, not an edit.
func listHiddenEdits(ctx context.Context, repoRoot string) ([]string, error) {
	out, err := runGit(ctx, repoRoot, "ls-files", "-z", "-v", "--stage")
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var entries strings.Builder
	sparse := make(map[string]bool)
	for _, record := range splitNUL(out) {
		// "<tag> <mode> <object> <stage>\t<path>"
		tag, entry, _ := strings.Cut(record, " ")
		meta, file, ok := strings.Cut(entry, "\t")
		fields := strings.Fields(meta)
		if !ok || len(fields) != 3 || fields[2] != "0" || !hiddenIndexTag(tag) || !worktreeEntryMode(fields[0]) {
			continue
		}
		sparse[file] = strings.EqualFold(tag, "S")
		entries.WriteString(entry)
		entries.WriteByte(0)
	}
	if len(sparse) == 0 {
		return nil, nil
	}
	changes, err := diffFilesWithoutFlags(ctx, repoRoot, entries.String())
	if err != nil {
		return nil, fmt.Errorf("compare files git treats as unchanged: %w", err)
	}
	var edited []string
	for file, status := range changes {
		if status != "D" || !sparse[file] {
			edited = append(edited, file)
		}
	}
	return edited, nil
}

// worktreeEntryMode reports whether an index mode is a file or symlink, the
// entries a worktree holds for this repository (not a submodule's gitlink).
func worktreeEntryMode(mode string) bool {
	return mode == "100644" || mode == "100755" || mode == "120000"
}

// diffFilesWithoutFlags copies entries (NUL-terminated `git ls-files --stage`
// records) into a temporary index, where they carry no assume-unchanged or
// skip-worktree flag, and returns git diff-files' status for each one whose
// worktree copy differs. git compares them itself, so clean filters and
// line-ending conversion apply as they do for git status. The real index is
// not touched.
func diffFilesWithoutFlags(ctx context.Context, repoRoot, entries string) (map[string]string, error) {
	dir, err := os.MkdirTemp("", "entire-review-index-")
	if err != nil {
		return nil, fmt.Errorf("create a temporary index: %w", err)
	}
	defer os.RemoveAll(dir)
	index, err := filepath.Abs(filepath.Join(dir, "index"))
	if err != nil {
		return nil, fmt.Errorf("create a temporary index: %w", err)
	}
	git := func(stdin string, args ...string) (string, error) {
		// A split index would leave a shared index file in the repository,
		// and fsmonitor could start its daemon for a throwaway index.
		cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.splitIndex=false", "-c", "core.fsmonitor=false"}, args...)...)
		cmd.Dir = repoRoot
		cmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+index)
		cmd.Stdin = strings.NewReader(stdin)
		var stderr strings.Builder
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return "", fmt.Errorf("git %s: %w (stderr: %s)", strings.Join(args[:2], " "), err, strings.TrimSpace(stderr.String()))
		}
		return string(out), nil
	}
	if _, err := git(entries, "update-index", "-z", "--index-info"); err != nil {
		return nil, err
	}
	// The copied entries have no stat data: refreshing compares each one's
	// contents and records the stat of those that match.
	if _, err := git("", "update-index", "-q", "--refresh"); err != nil {
		return nil, err
	}
	out, err := git("", "diff-files", "-z", "--name-status")
	if err != nil {
		return nil, err
	}
	// "<status>\0<path>\0" per entry.
	records := splitNUL(out)
	changes := make(map[string]string, len(records)/2)
	for i := 0; i+1 < len(records); i += 2 {
		changes[records[i+1]] = records[i]
	}
	return changes, nil
}

func splitNUL(out string) []string {
	out = strings.TrimRight(out, "\x00")
	if out == "" {
		return nil
	}
	return strings.Split(out, "\x00")
}

// TrailScope is what a trail-mode review's findings may anchor to, fixed
// before any reviewer runs. A finding quotes worktree lines only from a
// Quotable file whose contents are unchanged since then; a finding on any
// other covered file posts against that file without a quote. The zero value
// covers nothing.
type TrailScope struct {
	// Root is the worktree the scope was read from.
	Root string
	// Quotable maps each file the reviewed change adds or modifies to the
	// SHA-256 of its contents when the review started.
	Quotable map[string][sha256.Size]byte
	// Tracked holds every path in the index.
	Tracked map[string]struct{}
}

// CanQuote reports whether a finding may quote lines of the repo-relative
// path: the reviewed change adds or modifies it, and its contents are those
// the review started with.
func (s TrailScope) CanQuote(path string) bool {
	want, ok := s.Quotable[path]
	if !ok {
		return false
	}
	got, ok := hashWorktreeFile(s.Root, path)
	return ok && got == want
}

// Covers reports whether a finding may name the repo-relative path without
// quoting it.
func (s TrailScope) Covers(path string) bool {
	_, quotable := s.Quotable[path]
	_, tracked := s.Tracked[path]
	return quotable || tracked
}

func hashWorktreeFile(root, path string) ([sha256.Size]byte, bool) {
	data, err := worktreedir.ReadFileNoFollow(root, path)
	if err != nil {
		return [sha256.Size]byte{}, false
	}
	return sha256.Sum256(data), true
}

// trailScope computes the TrailScope of a review scoped to baseRef. Quotable
// files are those added or modified on the branch since it diverged from
// baseRef, plus every uncommitted change, except paths the uncommitted
// changes remove and paths whose local text git hides: an ignored local file
// can sit at a path the branch diff still lists. With no baseRef only
// uncommitted files are quotable.
func trailScope(ctx context.Context, repoRoot, baseRef string) (TrailScope, error) {
	if repoRoot == "" {
		return TrailScope{}, errors.New("no worktree root")
	}
	var changed []string
	if baseRef != "" {
		branch, err := listFilesChanged(ctx, repoRoot, baseRef)
		if err != nil {
			return TrailScope{}, err
		}
		changed = branch
	}
	uncommitted, removed, err := listUncommitted(ctx, repoRoot)
	if err != nil {
		return TrailScope{}, err
	}
	changed = append(changed, uncommitted...)
	tracked, hidden, err := listTracked(ctx, repoRoot)
	if err != nil {
		return TrailScope{}, err
	}
	excluded := hidden
	for _, file := range removed {
		excluded[file] = struct{}{}
	}
	scope := TrailScope{Root: repoRoot, Quotable: make(map[string][sha256.Size]byte, len(changed)), Tracked: tracked}
	for _, file := range changed {
		if _, ok := excluded[file]; ok {
			continue
		}
		if sum, ok := hashWorktreeFile(repoRoot, file); ok {
			scope.Quotable[file] = sum
		}
	}
	return scope, nil
}

// countUncommitted returns the number of lines in `git status --porcelain`
// (each line corresponds to one changed or untracked file).
func countUncommitted(ctx context.Context, repoRoot string) (int, error) {
	// --no-optional-locks keeps this a read: a bare `git status` rewrites
	// .git/index to refresh its stat cache (issue #2111).
	out, err := runGit(ctx, repoRoot, "--no-optional-locks", "status", "--porcelain")
	if err != nil {
		return 0, err
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return 0, nil
	}
	return len(strings.Split(trimmed, "\n")), nil
}

// runGit runs `git <args>` in repoDir and returns stdout as a string. Thin
// wrapper around gitexec.Run preserved so existing call sites don't change.
func runGit(ctx context.Context, repoRoot string, args ...string) (string, error) {
	return gitexec.Run(ctx, repoRoot, args...) //nolint:wrapcheck // gitexec already wraps
}
