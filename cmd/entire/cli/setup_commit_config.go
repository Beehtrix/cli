package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
)

// offerCommitConfig closes a completed enable by getting the configuration it
// wrote committed, so other worktrees and clones get the same setup. It finds
// whichever of sharedConfigFiles is untracked, modified, or deleted and, in an
// interactive terminal without --yes, offers to commit exactly those files;
// otherwise it lists them. In an empty repository it leads with why a commit
// is needed now. A failure to ask git skips it, and a failed commit is
// reported; the only error it returns is an interruption (Ctrl+C), so the
// command exits the way every other interrupted prompt does.
func offerCommitConfig(ctx context.Context, w io.Writer, opts EnableOptions) error {
	repo, err := strategy.OpenRepository(ctx)
	if err != nil {
		return nil
	}
	defer repo.Close()
	emptyRepo := strategy.IsEmptyRepository(repo)

	root, err := paths.WorktreeRoot(ctx)
	if err != nil {
		logging.Debug(ctx, "skipping commit-config offer: worktree root unresolved", "error", err)
		return nil
	}
	candidates := sharedConfigFiles()
	statuses, err := gitrepo.PathStatuses(ctx, root, candidates)
	if err != nil {
		logging.Debug(ctx, "skipping commit-config offer: git status failed", "error", err)
		return nil
	}
	var pending []string
	for _, file := range candidates {
		if _, ok := statuses[file]; ok {
			pending = append(pending, file)
		}
	}
	if len(pending) == 0 && !emptyRepo {
		return nil
	}

	fmt.Fprintln(w)
	if emptyRepo {
		fmt.Fprintln(w, "Session checkpoints require at least one commit.")
	}
	if len(pending) == 0 {
		return nil
	}
	if opts.Yes || opts.NoCommitOffer || !interactive.CanPromptInteractively() {
		printConfigToCommit(w, pending)
		return nil
	}

	branch := currentBranchName(repo)
	render, commit, err := commitConfigPrompt(ctx, os.Stderr, pending, branch)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("commit cancelled: %w", ctxErr)
	}
	if err != nil {
		if cerr := handleFormCancellation(render, "Commit", err); cerr != nil {
			// The prompt never showed (no terminal to open): fall back to
			// the list, which is all a non-interactive enable gets anyway.
			logging.Debug(ctx, "commit-config prompt failed", "error", cerr)
			printConfigToCommit(w, pending)
		}
		return nil
	}
	if !commit {
		return nil
	}
	if err := commitConfigFiles(ctx, root, pending, configCommitMessage, render); err != nil {
		fmt.Fprintf(render, "Could not commit them: %v\n", err)
		return nil
	}
	if branch != "" {
		fmt.Fprintf(render, "✓ Committed them to %s\n", branch)
	} else {
		fmt.Fprintln(render, "✓ Committed them")
	}
	return nil
}

// printConfigToCommit lists files with the reason to commit them.
func printConfigToCommit(w io.Writer, files []string) {
	fmt.Fprintln(w, "Make sure to commit these files to your Git repo so Entire works across worktrees and clones:")
	for _, file := range files {
		fmt.Fprintf(w, "  %s\n", file)
	}
}

// sharedConfigFiles returns the worktree-relative files setup writes or
// removes that are meant to be committed: the project settings,
// .entire/.gitignore, and for every registered agent — not only those with
// hooks installed now, so a configuration changed by deselecting an agent is
// included — its hook config and Entire's skill files (search skill,
// agent-help skill, and the legacy search subagent setup deletes). These paths
// are Entire's own, so one that is dirty is almost always setup's doing; the
// developer still sees the list before anything is committed.
func sharedConfigFiles() []string {
	files := []string{EntireSettingsFile, paths.EntireDir + "/.gitignore"}
	add := func(p string) {
		if p = filepath.ToSlash(p); p != "" && !slices.Contains(files, p) {
			files = append(files, p)
		}
	}
	for _, name := range agent.List() {
		if ag, err := agent.Get(name); err == nil {
			if locator, ok := agent.AsHookConfigLocator(ag); ok {
				add(locator.HookConfigRelPath())
			}
		}
		add(searchSkillTemplatePath(name))
		add(agentHelpSkillTemplatePath(name))
		add(legacySearchSubagentPath(name))
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

// promptCommitConfig lists files and asks whether to commit them to branch.
// The list and the question go to the same place through runPromptFormOn —
// errOut when it is a terminal, otherwise the controlling terminal — so the
// developer never answers about files they cannot see, whichever stream is
// redirected. render is where they went, for the outcome to follow.
func promptCommitConfig(ctx context.Context, errOut io.Writer, files []string, branch string) (render io.Writer, commit bool, err error) {
	title := "Commit them now? (HEAD is detached)"
	if branch != "" {
		title = fmt.Sprintf("Commit them to %s now?", branch)
	}
	commit = true
	form := NewAccessibleForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(title).
				Affirmative("Yes, commit").
				Negative("No").
				Value(&commit),
		),
	)
	render, err = runPromptFormOn(ctx, errOut, form, func(w io.Writer) error {
		printConfigToCommit(w, files)
		return nil
	})
	if err != nil {
		return render, false, err
	}
	return render, commit, nil
}
