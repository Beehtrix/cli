package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/huh/v2"
	"github.com/spf13/cobra"

	"github.com/entireio/cli/cmd/entire/cli/api"
	"github.com/entireio/cli/cmd/entire/cli/interactive"
	"github.com/entireio/cli/cmd/entire/cli/paths"
)

const projectAgentTuneLimitErrorMessage = "limit must be greater than 0"

// tuneMode is what one `project agent tune` invocation does.
type tuneMode string

const (
	// tuneModeApply tailors every agent in scope and saves the result.
	tuneModeApply tuneMode = "apply"
	// tuneModePrintPrompt prints the tailoring prompt for the caller's own
	// agent instead of running a provider. No API writes.
	tuneModePrintPrompt tuneMode = "print-prompt"
	// tuneModeDryRun tailors and prints the result as a diff, saving nothing.
	tuneModeDryRun tuneMode = "dry-run"
)

func (m tuneMode) needsProvider() bool { return m != tuneModePrintPrompt }

// presetTuneAction is what saving a tuned preset agent does. A preset agent's
// instructions are the preset's, so tuning one makes a custom agent — which
// runs only once the project's payer is enrolled in billing, while the preset
// runs on free credit.
type presetTuneAction string

const (
	// presetTuneCopy saves the tuning as a new custom agent, disabled, and
	// leaves the preset agent running.
	presetTuneCopy presetTuneAction = "copy"
	// presetTuneConvert replaces the preset agent with the tuned custom
	// version under the same id.
	presetTuneConvert presetTuneAction = "convert"
	// presetTuneSkip saves nothing for preset agents.
	presetTuneSkip presetTuneAction = "skip"
)

func parsePresetTuneAction(s string) (presetTuneAction, error) {
	switch a := presetTuneAction(strings.ToLower(strings.TrimSpace(s))); a {
	case "", presetTuneCopy, presetTuneConvert, presetTuneSkip:
		return a, nil
	}
	return "", fmt.Errorf("--presets must be copy, convert or skip, not %q", s)
}

// sharedTuneAction is what saving the tuning of an agent that also reviews
// other repositories does. The tuning is written for this repository, so
// saving it in place changes how the agent reviews the others too.
type sharedTuneAction string

const (
	// sharedTuneUpdate saves the tuning on the shared agent: every repository
	// it reviews gets this repository's instructions.
	sharedTuneUpdate sharedTuneAction = "update"
	// sharedTuneCopy saves the tuning as a new custom agent that reviews only
	// this repository, disabled, and leaves the shared agent unchanged.
	sharedTuneCopy sharedTuneAction = "copy"
	// sharedTuneSkip saves nothing for shared agents.
	sharedTuneSkip sharedTuneAction = "skip"
)

func parseSharedTuneAction(s string) (sharedTuneAction, error) {
	switch a := sharedTuneAction(strings.ToLower(strings.TrimSpace(s))); a {
	case "", sharedTuneUpdate, sharedTuneCopy, sharedTuneSkip:
		return a, nil
	}
	return "", fmt.Errorf("--shared must be update, copy or skip, not %q", s)
}

// tuneSave is what saving one tuned agent does, once both answers are known.
type tuneSave int

const (
	tuneSaveSkip    tuneSave = iota
	tuneSaveInPlace          // replace the agent (a preset agent becomes custom)
	tuneSaveCopy             // new custom agent for this repository only, disabled
)

type projectAgentTuneOptions struct {
	agent       string // optional: limit to one agent (id, name or preset id)
	assumeYes   bool
	printPrompt bool
	dryRun      bool
	presets     string
	shared      string
	provider    string // --provider: text-generation agent for this run; saves nothing
	debugDir    string
	sources     []string
	limit       int
}

func newProjectAgentTuneCmd() *cobra.Command {
	var opts projectAgentTuneOptions
	cmd := &cobra.Command{
		Use:   "tune [<agent>]",
		Short: "Tailor the project agents' instructions to this repository",
		Long: `Rewrite the instructions of the project agents that review this repository so
their focus fits it, using gathered signal — its docs and structure, merged PRs
and issues, checkpoint churn hotspots, and past trail findings. One call to your
configured summary provider tailors every agent in scope.

In a terminal, tune shows the changes as a diff and asks before saving. Otherwise
name the action:

  -y, --yes           save the tuning without asking
      --dry-run       show the tuning as a diff and save nothing
      --print-prompt  print the tailoring prompt for your own agent to run

A preset agent's instructions belong to its Entire preset, so a tuned preset
agent becomes a custom agent. Custom agents run only once the project's payer is
enrolled in billing; preset agents run on free credit. For tuned preset agents,
tune asks whether to:

  copy     add the tuning as a new custom agent, disabled, and keep the preset
           agent running (enable the copy once billing is set up)
  convert  replace the preset agent with the tuned custom version
  skip     leave preset agents unchanged

Pass --presets copy|convert|skip to answer up front; it is required with --yes
when a preset agent is in scope and no terminal can ask.

An agent can review several repositories, and the tuning is written for this
one. When a tuned agent also reviews other repositories — a custom agent, or a
preset agent being converted — tune lists them and asks whether to:

  update   save the tuning on the shared agent, changing its reviews in every
           repository it reviews
  copy     add the tuning as a new custom agent that reviews only this
           repository, disabled, and leave the shared agent unchanged
  skip     leave shared agents unchanged

Pass --shared update|copy|skip to answer up front; it is required with --yes
when such an agent is in scope and no terminal can ask.

Copies always review only this repository. They are added disabled because the
original keeps reviewing it. To switch over, enable the copy, then archive an
original that reviews only this repository, or remove this repository from one
that also reviews others (an agent needs at least one repository).

With no provider configured and several agents installed, you are asked which to
use; pass --provider <name> to name one for this run instead (nothing is saved).

If <agent> is given (an agent id, name or preset id), only that agent is tuned.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.SilenceUsage = true
			if len(args) == 1 {
				opts.agent = args[0]
			}
			return runProjectAgentTune(cmd, opts)
		},
	}

	cmd.Flags().BoolVarP(&opts.assumeYes, "yes", "y", false, "Save the tuning without asking")
	cmd.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Show the tuning as a diff and save nothing")
	cmd.Flags().BoolVar(&opts.printPrompt, "print-prompt", false,
		"Print the tailoring prompt for your own agent instead of running a provider; saves nothing")
	cmd.Flags().StringVar(&opts.presets, "presets", "",
		"What to do with tuned preset agents: copy (new disabled custom agent), convert (replace the preset agent), or skip")
	cmd.Flags().StringVar(&opts.shared, "shared", "",
		"What to do with tuned agents that also review other repositories: update (all of them), copy (new disabled agent for this repository only), or skip")
	cmd.Flags().StringVar(&opts.provider, "provider", "",
		"Text-generation agent to tune with for this run (e.g. codex); skips the provider choice and saves nothing")
	cmd.Flags().StringSliceVar(&opts.sources, "sources", nil,
		"Comma-separated data sources to gather: repo, prs, checkpoints, trails, all (default: all)")
	cmd.Flags().IntVar(&opts.limit, "limit", 20, "How many recent PRs/issues/trails to sample")
	cmd.Flags().StringVar(&opts.debugDir, "debug-dir", "",
		"Write the assembled prompt (prompt.txt) and, when a provider runs, its raw response (response.txt) to this directory for debugging")
	cmd.MarkFlagsMutuallyExclusive("yes", "print-prompt", "dry-run")
	return cmd
}

// tuneUsage is a tune invocation's flags, validated.
type tuneUsage struct {
	mode         tuneMode
	ask          bool // a terminal answers the questions left to the end
	presetAction presetTuneAction
	sharedAction sharedTuneAction
	src          tuneSources
}

// parseTuneUsage settles usage before any I/O: a bad flag must not cost a
// lookup.
func parseTuneUsage(opts projectAgentTuneOptions) (tuneUsage, error) {
	u := tuneUsage{mode: tuneModeApply}
	switch {
	case opts.dryRun:
		u.mode = tuneModeDryRun
	case opts.printPrompt:
		u.mode = tuneModePrintPrompt
	}
	u.ask = u.mode == tuneModeApply && !opts.assumeYes && interactive.CanPromptInteractively()
	if u.mode == tuneModeApply && !opts.assumeYes && !u.ask {
		return u, errors.New("no terminal to confirm saving: pass --yes, --dry-run, or --print-prompt — see --help")
	}
	if opts.provider != "" && !u.mode.needsProvider() {
		return u, errors.New("--provider names the agent that tunes, but --print-prompt runs none; drop --provider")
	}
	var err error
	if u.presetAction, err = parsePresetTuneAction(opts.presets); err != nil {
		return u, err
	}
	if u.sharedAction, err = parseSharedTuneAction(opts.shared); err != nil {
		return u, err
	}
	if u.src, err = parseTuneSources(opts.sources); err != nil {
		return u, err
	}
	if opts.limit <= 0 {
		return u, errors.New(projectAgentTuneLimitErrorMessage)
	}
	return u, nil
}

func runProjectAgentTune(cmd *cobra.Command, opts projectAgentTuneOptions) error {
	ctx := cmd.Context()
	w, errW := cmd.OutOrStdout(), cmd.ErrOrStderr()

	u, err := parseTuneUsage(opts)
	if err != nil {
		return err
	}
	mode, ask, presetAction, sharedAction := u.mode, u.ask, u.presetAction, u.sharedAction

	repoRoot, err := paths.WorktreeRoot(ctx)
	if err != nil {
		return fmt.Errorf("not a git repository: %w", err)
	}
	t, err := resolveProjectAgentTarget(cmd)
	if err != nil {
		return err
	}
	repoID, repoLabel, err := t.currentRepoID(cmd)
	if err != nil {
		return err
	}
	inRepo, err := t.listAgents(ctx, repoID)
	if err != nil {
		return err
	}
	agents, err := filterTuneAgents(inRepo, opts.agent)
	if err != nil {
		return err
	}
	if len(agents) == 0 {
		return fmt.Errorf("no agent in %s reviews %s; add one with `entire project agent add`", t.label(), repoLabel)
	}
	// Asked at the end, but required up front when nothing can ask: a run
	// must not spend a provider call and then stop on a missing answer.
	scope := tuneScope{repoID: repoID, repoName: repoNameOf(repoLabel), names: resolveProjectRepoNames(ctx, t.projectID)}
	if mode == tuneModeApply && !ask {
		if presetAction == "" && slices.ContainsFunc(agents, isPresetAgent) {
			return errors.New("a preset agent is in scope and no terminal can ask what to do with its tuning: pass --presets copy, convert or skip")
		}
		if shared := scope.sharedAgents(agents, presetAction); sharedAction == "" && len(shared) > 0 {
			return fmt.Errorf("these agents also review other repositories, and the tuning is written for %s:\n%s"+
				"pass --shared update (change their reviews everywhere), copy (new agent for %s only) or skip",
				scope.repoName, scope.describe(shared), scope.repoName)
		}
	}

	// Resolved before the gather: resolution can fail outright, or stop to ask
	// which provider to use, and neither belongs after seconds of waiting.
	var provider *checkpointSummaryProvider
	if mode.needsProvider() {
		if provider, err = resolveDispatchSummaryProvider(ctx, errW, opts.provider); err != nil {
			return err
		}
	}

	stopGather := startSpinner(errW, "Gathering repository signal")
	brief := gatherTuningContext(ctx, errW, repoRoot, u.src, opts.limit, insecureHTTPRequested(cmd))
	stopGather(true)
	prompt := buildTunePrompt(brief, agents)
	if opts.debugDir != "" {
		writeTuneDebug(errW, opts.debugDir, "prompt.txt", prompt)
	}

	if mode == tuneModePrintPrompt {
		fmt.Fprintln(w, prompt)
		fmt.Fprintf(errW, "\n%d agent(s) in scope. Paste the prompt above into your agent, or re-run with --yes to save the tuning.\n", len(agents))
		return nil
	}

	changes, skipped, err := runTuning(ctx, errW, provider, agents, prompt, opts.debugDir)
	if err != nil {
		return err
	}
	if mode == tuneModeDryRun || ask {
		previewTunedAgents(w, scope, changes)
	}
	if len(changes) == 0 {
		if skipped > 0 {
			return fmt.Errorf("model proposed %d change(s) but all were rejected or out of scope (see messages above)", skipped)
		}
		fmt.Fprintln(errW, "No agent changes proposed.")
		return nil
	}
	if mode == tuneModeDryRun {
		fmt.Fprintf(errW, "%d of %d agent(s) would change. Nothing was saved — re-run with --yes to save.\n", len(changes), len(agents))
		return nil
	}

	plan, err := planTunedSaves(ctx, errW, ask, scope, changes, presetAction, sharedAction)
	if err != nil || plan == nil {
		return err // a nil plan without an error is a cancelled picker
	}
	return saveTunedAgents(ctx, w, errW, t, scope, changes, plan)
}

// planTunedSaves settles what saving each tuned agent does, asking a
// terminal the questions its flags left open. The preset answer comes first:
// whether a preset agent is shared only matters if it is being converted.
func planTunedSaves(ctx context.Context, errW io.Writer, ask bool, scope tuneScope, changes []tunedAgent, presetAction presetTuneAction, sharedAction sharedTuneAction) ([]tuneSave, error) {
	var err error
	if presetAction == "" && slices.ContainsFunc(changes, func(c tunedAgent) bool { return isPresetAgent(c.agent) }) {
		if presetAction, err = choosePresetTuneAction(ctx, errW); err != nil || presetAction == "" {
			return nil, err
		}
	}
	tuned := make([]api.ProjectAgent, len(changes))
	for i, c := range changes {
		tuned[i] = c.agent
	}
	if shared := scope.sharedAgents(tuned, presetAction); len(shared) > 0 {
		switch sharedAction {
		case "":
			if sharedAction, err = chooseSharedTuneAction(ctx, errW, scope, shared); err != nil || sharedAction == "" {
				return nil, err
			}
		case sharedTuneUpdate:
			fmt.Fprintf(errW, "Saving on shared agents changes their reviews in every repository they review:\n%s", scope.describe(shared))
		case sharedTuneCopy, sharedTuneSkip:
		}
	}

	// Custom agents reviewing only this repository are saved in place; a
	// terminal confirms that, since nothing above asked about them.
	plan := make([]tuneSave, len(changes))
	n := 0
	for i, c := range changes {
		plan[i] = scope.save(c.agent, presetAction, sharedAction)
		if plan[i] == tuneSaveInPlace && scope.soloCustom(c.agent) {
			n++
		}
	}
	if ask && n > 0 {
		ok, err := confirmTuneCustom(ctx, errW, n)
		if err != nil {
			return nil, err
		}
		for i, c := range changes {
			if !ok && plan[i] == tuneSaveInPlace && scope.soloCustom(c.agent) {
				plan[i] = tuneSaveSkip
			}
		}
	}
	return plan, nil
}

// soloCustom is a custom agent that reviews only this repository: the one
// kind saved in place without a scope or billing question.
func (s tuneScope) soloCustom(a api.ProjectAgent) bool { return !isPresetAgent(a) && !s.isShared(a) }

// tuneScope is the repository a tuning is written for, and the names of the
// project's repositories for messages about the others.
type tuneScope struct {
	repoID   string
	repoName string
	names    map[string]string
}

// otherRepos is the rest of a's live selection: the repositories whose
// reviews change when a's tuning is saved in place.
func (s tuneScope) otherRepos(a api.ProjectAgent) []string {
	var out []string
	for _, id := range liveRepositoryIDs(a) {
		if id != s.repoID {
			out = append(out, id)
		}
	}
	return out
}

func (s tuneScope) isShared(a api.ProjectAgent) bool { return len(s.otherRepos(a)) > 0 }

// sharedAgents are the agents among agents whose in-place save would reach
// other repositories: shared custom agents, and shared preset agents when
// they are being converted. A preset copy never reaches them.
func (s tuneScope) sharedAgents(agents []api.ProjectAgent, presetAction presetTuneAction) []api.ProjectAgent {
	var out []api.ProjectAgent
	for _, a := range agents {
		if !s.isShared(a) {
			continue
		}
		if isPresetAgent(a) && presetAction != presetTuneConvert {
			continue
		}
		out = append(out, a)
	}
	return out
}

// describe lists each agent with the other repositories it reviews.
func (s tuneScope) describe(agents []api.ProjectAgent) string {
	var b strings.Builder
	for _, a := range agents {
		fmt.Fprintf(&b, "  %s also reviews: %s\n", a.Name, repoNames(s.names, s.otherRepos(a)))
	}
	return b.String()
}

// save settles what saving a does from the two answers.
func (s tuneScope) save(a api.ProjectAgent, presetAction presetTuneAction, sharedAction sharedTuneAction) tuneSave {
	if isPresetAgent(a) {
		switch presetAction {
		case presetTuneCopy:
			return tuneSaveCopy
		case presetTuneSkip, "":
			return tuneSaveSkip
		case presetTuneConvert:
		}
	}
	if !s.isShared(a) {
		return tuneSaveInPlace
	}
	switch sharedAction {
	case sharedTuneUpdate:
		return tuneSaveInPlace
	case sharedTuneCopy:
		return tuneSaveCopy
	case sharedTuneSkip, "":
	}
	return tuneSaveSkip
}

// repoNameOf is the repository name of a forge/owner/repo label.
func repoNameOf(label string) string {
	if i := strings.LastIndexByte(label, '/'); i >= 0 {
		return label[i+1:]
	}
	return label
}

func isPresetAgent(a api.ProjectAgent) bool { return a.Preset != nil }

// filterTuneAgents keeps the agent selector's match. An exact agent id wins;
// otherwise the selector must name one agent, by name or preset id (ignoring
// case). A custom agent's name can equal another agent's preset id, so a
// selector matching more than one is refused rather than resolved by list
// order. No selector keeps every agent.
func filterTuneAgents(agents []api.ProjectAgent, selector string) ([]api.ProjectAgent, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return agents, nil
	}
	for _, a := range agents {
		if a.ID == selector {
			return []api.ProjectAgent{a}, nil
		}
	}
	var matches []api.ProjectAgent
	for _, a := range agents {
		if strings.EqualFold(a.Name, selector) || (a.Preset != nil && strings.EqualFold(a.Preset.ID, selector)) {
			matches = append(matches, a)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no agent matching %q reviews this repository", selector)
	case 1:
		return matches, nil
	}
	var ids []string
	for _, a := range matches {
		ids = append(ids, fmt.Sprintf("%s (%s, %s)", a.ID, a.Name, projectAgentPresetLabel(a)))
	}
	return nil, fmt.Errorf("%q matches %d agents; select one by id: %s", selector, len(matches), strings.Join(ids, "; "))
}

// runTuning runs the prompt through an already-resolved summary provider and
// turns the agent-id -> instructions map it returns into accepted rewrites.
// Nothing is saved here.
func runTuning(ctx context.Context, errW io.Writer, provider *checkpointSummaryProvider, agents []api.ProjectAgent, prompt, debugDir string) (changes []tunedAgent, skipped int, err error) {
	// provider.TextGenerator is the plain prompt->text generator and is never
	// nil: its constructor fails when the agent has none.
	stop := startSpinner(errW, fmt.Sprintf("Tuning %d agent(s) with %s", len(agents), provider.DisplayName))
	out, err := provider.TextGenerator.GenerateText(ctx, prompt, provider.Model)
	stop(err == nil)
	if err != nil {
		return nil, 0, fmt.Errorf("agent run failed: %w", err)
	}
	if debugDir != "" {
		writeTuneDebug(errW, debugDir, "response.txt", out)
	}
	proposals, err := parseTuneOutput(out)
	if err != nil {
		return nil, 0, err
	}
	changes, skipped = classifyTuneProposals(errW, agents, proposals)
	return changes, skipped, nil
}

func previewTunedAgents(w io.Writer, scope tuneScope, changes []tunedAgent) {
	for _, c := range changes {
		kind := projectAgentCustomLabel
		if c.agent.Preset != nil {
			kind = "preset " + c.agent.Preset.ID
		}
		fmt.Fprintf(w, "=== %s (%s, %s) — instructions would change ===\n", c.agent.Name, kind, c.agent.ID)
		if others := scope.otherRepos(c.agent); len(others) > 0 {
			fmt.Fprintf(w, "    also reviews: %s\n", repoNames(scope.names, others))
		}
		fmt.Fprint(w, renderTextDiff(c.agent.Instructions, c.instructions))
		fmt.Fprintln(w)
	}
}

func confirmTuneCustom(ctx context.Context, errW io.Writer, n int) (bool, error) {
	ok := true
	form := NewAccessibleForm(huh.NewGroup(
		huh.NewConfirm().
			Title(fmt.Sprintf("Save the tuning of %d custom agent(s) that review only this repository?", n)).
			Value(&ok),
	))
	if err := form.RunWithContext(ctx); err != nil {
		return false, handleFormCancellation(errW, "Tuning", err)
	}
	return ok, nil
}

// choosePresetTuneAction asks, after the diff, what to do with tuned preset
// agents. It is left to the end because the answer depends on billing: the
// copy keeps the free preset running.
func choosePresetTuneAction(ctx context.Context, errW io.Writer) (presetTuneAction, error) {
	action := presetTuneCopy
	form := NewAccessibleForm(huh.NewGroup(
		huh.NewSelect[presetTuneAction]().
			Title("Preset agents were tuned. How should their tuning be saved?").
			Description("A tuned preset agent is a custom agent: it runs only once the project's payer is enrolled in billing. Preset agents run on free credit.").
			Options(
				huh.NewOption("Add a new custom agent for this repository only, disabled, and keep the preset running", presetTuneCopy),
				huh.NewOption("Replace each preset agent with its tuned custom version", presetTuneConvert),
				huh.NewOption("Leave the preset agents unchanged", presetTuneSkip),
			).
			Value(&action),
	))
	if err := form.RunWithContext(ctx); err != nil {
		return "", handleFormCancellation(errW, "Tuning", err)
	}
	return action, nil
}

// chooseSharedTuneAction asks what to do with tuned agents that also review
// other repositories, naming them, so a tuning written for this repository
// does not silently change the others' reviews.
func chooseSharedTuneAction(ctx context.Context, errW io.Writer, scope tuneScope, shared []api.ProjectAgent) (sharedTuneAction, error) {
	action := sharedTuneCopy
	form := NewAccessibleForm(huh.NewGroup(
		huh.NewSelect[sharedTuneAction]().
			Title(fmt.Sprintf("These agents also review other repositories, and the tuning is written for %s:\n%s", scope.repoName, strings.TrimRight(scope.describe(shared), "\n"))).
			Options(
				huh.NewOption("Add a new custom agent for "+scope.repoName+" only, disabled, and leave the shared agents unchanged", sharedTuneCopy),
				huh.NewOption("Save on the shared agents, changing their reviews in every repository listed", sharedTuneUpdate),
				huh.NewOption("Leave the shared agents unchanged", sharedTuneSkip),
			).
			Value(&action),
	))
	if err := form.RunWithContext(ctx); err != nil {
		return "", handleFormCancellation(errW, "Tuning", err)
	}
	return action, nil
}

// saveTunedAgents writes each tuned agent as plan says. Replaces are
// compare-and-swap on the etag read before tuning, so a concurrent edit is
// refused rather than overwritten.
func saveTunedAgents(ctx context.Context, w, errW io.Writer, t *projectAgentTarget, scope tuneScope, changes []tunedAgent, plan []tuneSave) error {
	var taken []string
	if slices.Contains(plan, tuneSaveCopy) {
		all, err := t.listAgents(ctx, "")
		if err != nil {
			return err
		}
		for _, a := range all {
			taken = append(taken, a.Name)
		}
	}

	var saved, converted, skipped int
	var followUps []string
	for i, c := range changes {
		switch plan[i] {
		case tuneSaveInPlace:
			if _, err := t.replaceAgent(ctx, c.agent.ID, c.agent.ETag, tunedAgentWrite(errW, c.agent, c.instructions)); err != nil {
				return fmt.Errorf("save %s: %w", c.agent.Name, err)
			}
			if isPresetAgent(c.agent) {
				fmt.Fprintf(w, "converted %s to a custom agent\n", c.agent.Name)
				converted++
			} else {
				fmt.Fprintf(w, "updated %s\n", c.agent.Name)
			}
			saved++
		case tuneSaveCopy:
			body := tunedAgentWrite(errW, c.agent, c.instructions)
			body.Name = uniqueAgentName(c.agent.Name, " ("+scope.repoName+")", taken)
			body.IsEnabled = false
			body.RepositoryIDs = []string{scope.repoID}
			a, err := t.createAgent(ctx, body)
			if err != nil {
				return fmt.Errorf("add tuned copy of %s: %w", c.agent.Name, err)
			}
			taken = append(taken, a.Name)
			fmt.Fprintf(w, "added %s (%s) for %s only, disabled\n", a.Name, a.ID, scope.repoName)
			followUps = append(followUps, copyFollowUp(scope, c.agent, a.Name))
			saved++
		case tuneSaveSkip:
			skipped++
		}
	}

	fmt.Fprintf(errW, "\nSaved %d agent(s) in %s", saved, t.label())
	if skipped > 0 {
		fmt.Fprintf(errW, "; left %d unchanged", skipped)
	}
	fmt.Fprintln(errW, ".")
	if len(followUps) > 0 {
		fmt.Fprintf(errW, "Copies are disabled because their originals still review %s. Custom agents run once the project's payer is enrolled in billing. Then:\n", scope.repoName)
		for _, f := range followUps {
			fmt.Fprintf(errW, "  %s\n", f)
		}
	}
	if converted > 0 {
		fmt.Fprintf(errW, "The %d converted agent(s) no longer follow their preset and run only once the project's payer is enrolled in billing.\n", converted)
	}
	return nil
}

// copyFollowUp says how to hand this repository's reviews from original to
// its copy. An agent needs at least one repository, so an original that
// reviews only this one is archived, not emptied.
func copyFollowUp(scope tuneScope, original api.ProjectAgent, copyName string) string {
	if scope.isShared(original) {
		return fmt.Sprintf("enable %s, and remove %s from %s's repositories (it keeps reviewing %s)",
			copyName, scope.repoName, original.Name, repoNames(scope.names, scope.otherRepos(original)))
	}
	return fmt.Sprintf("enable %s, and archive %s (it reviews only %s)", copyName, original.Name, scope.repoName)
}

// tunedAgentWrite is a's full custom definition with new instructions: the
// replace body for an in-place save, and the base of a copy's create body.
// A selected repository the project no longer owns is dropped — the API
// refuses a write that keeps one.
func tunedAgentWrite(errW io.Writer, a api.ProjectAgent, instructions string) api.ProjectAgentWrite {
	signal := a.Signal
	repos := liveRepositoryIDs(a)
	if len(repos) < len(a.RepositoryIDs) {
		fmt.Fprintf(errW, "note: dropping %d repositor(ies) that left the project from %s\n", len(a.RepositoryIDs)-len(repos), a.Name)
	}
	return api.ProjectAgentWrite{
		Name:            a.Name,
		Description:     a.Description,
		IsEnabled:       a.IsEnabled,
		Harness:         a.Harness,
		Model:           a.Model,
		ReasoningEffort: a.ReasoningEffort,
		TimeoutMS:       a.TimeoutMS,
		Instructions:    instructions,
		Signal:          &signal,
		RepositoryIDs:   repos,
		Triggers:        a.Triggers,
		EnvironmentID:   a.EnvironmentID,
	}
}

// projectAgentMaxName is the API's limit on an agent name, in bytes.
const projectAgentMaxName = 128

// uniqueAgentName is base+suffix, numbered if needed, unique among taken
// ignoring case — the API's rule for active agents — and within the API's
// name limit: base is shortened (on a rune boundary) rather than the save
// failing after the provider call it follows.
func uniqueAgentName(base, suffix string, taken []string) string {
	isTaken := func(n string) bool {
		return slices.ContainsFunc(taken, func(t string) bool { return strings.EqualFold(t, n) })
	}
	fit := func(tail string) string {
		for len(tail) > projectAgentMaxName {
			_, size := utf8.DecodeLastRuneInString(tail)
			tail = tail[:len(tail)-size]
		}
		b := base
		for len(b)+len(tail) > projectAgentMaxName && b != "" {
			_, size := utf8.DecodeLastRuneInString(b)
			b = b[:len(b)-size]
		}
		return strings.TrimRight(b, " ") + tail
	}
	if n := fit(suffix); !isTaken(n) {
		return n
	}
	for i := 2; ; i++ {
		if n := fit(fmt.Sprintf("%s %d", suffix, i)); !isTaken(n) {
			return n
		}
	}
}

// writeTuneDebug best-effort writes content to <dir>/<name> for debugging,
// reporting any failure as a warning rather than failing the command.
func writeTuneDebug(errW io.Writer, dir, name, content string) {
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // user-specified debug dir
		fmt.Fprintf(errW, "warning: debug dir %s: %v\n", dir, err)
		return
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // local debug artifact
		fmt.Fprintf(errW, "warning: writing %s: %v\n", path, err)
		return
	}
	fmt.Fprintf(errW, "debug: wrote %s\n", path)
}
