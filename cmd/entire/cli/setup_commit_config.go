package cli

import (
	"context"
	"fmt"
	"io"
	"slices"

	"charm.land/huh/v2"
	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/gitrepo"
	"github.com/entireio/cli/cmd/entire/cli/interactive"
	"github.com/entireio/cli/cmd/entire/cli/logging"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/strategy"
)

// configCommitMessage is the message of the commit enable offers to make.
const configCommitMessage = "Enable Entire"

// Overridable in tests.
var (
	commitConfigPrompt = promptCommitConfig
	commitConfigFiles  = gitrepo.CommitPaths
	// commitConfigCanPrompt reports whether the offer can be asked where the
	// developer sees it: the file list goes to w, so w must be the terminal
	// too, not only some controlling terminal behind redirected output.
	commitConfigCanPrompt = func(w io.Writer) bool {
		return interactive.CanPromptInteractively() && interactive.IsTerminalWriter(w)
	}
)

// offerCommitConfig closes a completed enable by getting the configuration it
// wrote committed, so other worktrees and clones get the same setup. It finds
// whichever of sharedConfigFiles is untracked or modified, lists them, and,
// in an interactive terminal without --yes, offers to commit exactly those
// files. In an empty repository it leads with why a commit is needed now. It
// is best-effort and never fails enable; a failure to ask git skips it.
func offerCommitConfig(ctx context.Context, w io.Writer, opts EnableOptions) {
	repo, err := strategy.OpenRepository(ctx)
	if err != nil {
		return
	}
	defer repo.Close()
	emptyRepo := strategy.IsEmptyRepository(repo)

	root, err := paths.WorktreeRoot(ctx)
	if err != nil {
		logging.Debug(ctx, "skipping commit-config offer: worktree root unresolved", "error", err)
		return
	}
	candidates := sharedConfigFiles(ctx)
	statuses, err := gitrepo.PathStatuses(ctx, root, candidates)
	if err != nil {
		logging.Debug(ctx, "skipping commit-config offer: git status failed", "error", err)
		return
	}
	var pending []string
	for _, file := range candidates {
		if _, ok := statuses[file]; ok {
			pending = append(pending, file)
		}
	}
	if len(pending) == 0 && !emptyRepo {
		return
	}

	fmt.Fprintln(w)
	if emptyRepo {
		fmt.Fprintln(w, "Session checkpoints require at least one commit.")
	}
	if len(pending) == 0 {
		return
	}

	fmt.Fprintln(w, "Make sure to commit these files to your Git repo so Entire works across worktrees and clones:")
	for _, file := range pending {
		fmt.Fprintf(w, "  %s\n", file)
	}
	if opts.Yes || !commitConfigCanPrompt(w) {
		return
	}

	branch := currentBranchName(repo)
	commit, err := commitConfigPrompt(branch)
	if err != nil {
		logging.Debug(ctx, "commit-config prompt failed", "error", err)
		return
	}
	if !commit {
		return
	}
	if err := commitConfigFiles(ctx, root, pending, configCommitMessage, w); err != nil {
		fmt.Fprintf(w, "Could not commit them: %v\n", err)
		return
	}
	if branch != "" {
		fmt.Fprintf(w, "✓ Committed them to %s\n", branch)
	} else {
		fmt.Fprintln(w, "✓ Committed them")
	}
}

// sharedConfigFiles returns the worktree-relative files enable writes that are
// meant to be committed: the project settings, .entire/.gitignore, and the
// hook config of each agent with hooks installed.
func sharedConfigFiles(ctx context.Context) []string {
	files := []string{EntireSettingsFile, paths.EntireDir + "/.gitignore"}
	for _, name := range GetAgentsWithHooksInstalled(ctx) {
		ag, err := agent.Get(name)
		if err != nil {
			continue
		}
		if locator, ok := agent.AsHookConfigLocator(ag); ok {
			if p := locator.HookConfigRelPath(); p != "" && !slices.Contains(files, p) {
				files = append(files, p)
			}
		}
	}
	return files
}

// currentBranchName returns the branch HEAD points at, including an unborn
// one, or "" for a detached HEAD.
func currentBranchName(repo *git.Repository) string {
	head, err := repo.Reference(plumbing.HEAD, false)
	if err != nil || head.Type() != plumbing.SymbolicReference || !head.Target().IsBranch() {
		return ""
	}
	return head.Target().Short()
}

// promptCommitConfig asks whether to commit the files just listed to branch.
func promptCommitConfig(branch string) (bool, error) {
	title := "Commit them now? (HEAD is detached)"
	if branch != "" {
		title = fmt.Sprintf("Commit them to %s now?", branch)
	}
	commit := true
	form := NewAccessibleForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(title).
				Affirmative("Yes, commit").
				Negative("No").
				Value(&commit),
		),
	)
	if err := form.Run(); err != nil {
		return false, fmt.Errorf("commit prompt: %w", err)
	}
	return commit, nil
}
