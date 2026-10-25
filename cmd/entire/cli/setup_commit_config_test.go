package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
	"testing"

	"charm.land/huh/v2"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/interactive"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

const testEntireGitignore = ".entire/.gitignore"

// newCommitConfigRepo chdirs into a fresh repository with git isolated from
// the developer's global and system config, commits a README unless empty is
// set, and leaves .entire/settings.json untracked.
func newCommitConfigRepo(t *testing.T, empty bool) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := setupTestRepo(t)
	if !empty {
		testutil.WriteFile(t, dir, "README.md", "x")
		testutil.GitAdd(t, dir, "README.md")
		testutil.GitCommit(t, dir, "initial")
	}
	testutil.WriteFile(t, dir, EntireSettingsFile, `{"enabled":true}`)
	return dir
}

type commitConfigPromptFunc = func(context.Context, io.Writer, []string, string) (io.Writer, bool, error)

// stubCommitConfig replaces the prompt and, when commit is non-nil, the commit
// for one test.
func stubCommitConfig(t *testing.T, prompt commitConfigPromptFunc, commit func(context.Context, string, []string, string, io.Writer) error) {
	t.Helper()
	origPrompt, origCommit := commitConfigPrompt, commitConfigFiles
	t.Cleanup(func() { commitConfigPrompt, commitConfigFiles = origPrompt, origCommit })
	commitConfigPrompt = prompt
	if commit != nil {
		commitConfigFiles = commit
	}
}

// promptedCommitConfig is a stub prompt that renders like the real one — the
// list, on render — and answers with commit and err.
type promptedCommitConfig struct {
	render *bytes.Buffer
	commit bool
	err    error

	asked  bool
	files  []string
	branch string
}

func (p *promptedCommitConfig) prompt(_ context.Context, _ io.Writer, files []string, branch string) (io.Writer, bool, error) {
	p.asked, p.files, p.branch = true, files, branch
	printConfigToCommit(p.render, files)
	return p.render, p.commit, p.err
}

func failOnPrompt(t *testing.T) commitConfigPromptFunc {
	t.Helper()
	return func(context.Context, io.Writer, []string, string) (io.Writer, bool, error) {
		t.Error("prompted to commit when no prompt was expected")
		return io.Discard, false, nil
	}
}

func assertOutput(t *testing.T, out string, want, notWant []string) {
	t.Helper()
	for _, s := range want {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q:\n%s", s, out)
		}
	}
	for _, s := range notWant {
		if strings.Contains(out, s) {
			t.Errorf("output unexpectedly contains %q:\n%s", s, out)
		}
	}
}

func runOfferCommitConfig(t *testing.T, opts EnableOptions) string {
	t.Helper()
	var out bytes.Buffer
	if err := offerCommitConfig(context.Background(), &out, opts); err != nil {
		t.Fatalf("offerCommitConfig() error = %v", err)
	}
	return out.String()
}

// Not parallel: newCommitConfigRepo chdirs and sets environment variables.
func TestOfferCommitConfig_ListsPendingFilesWithoutATerminal(t *testing.T) {
	tests := []struct {
		name    string
		empty   bool
		prepare func(t *testing.T, dir string)
		want    []string
		notWant []string
	}{
		{
			name:    "untracked settings",
			want:    []string{"Make sure to commit these files", "  .entire/settings.json\n"},
			notWant: []string{testEntireGitignore, "Session checkpoints"},
		},
		{
			name: "modified settings and untracked gitignore",
			prepare: func(t *testing.T, dir string) {
				t.Helper()
				testutil.GitAdd(t, dir, EntireSettingsFile)
				testutil.GitCommit(t, dir, "settings")
				testutil.WriteFile(t, dir, EntireSettingsFile, `{"enabled":false}`)
				testutil.WriteFile(t, dir, testEntireGitignore, "tmp/\n")
			},
			want: []string{"  .entire/settings.json\n  .entire/.gitignore\n"},
		},
		{
			name: "installed agent hook config",
			prepare: func(t *testing.T, _ string) {
				t.Helper()
				ag, err := agent.Get(agent.AgentNameClaudeCode)
				if err != nil {
					t.Fatalf("get Claude Code agent: %v", err)
				}
				hookAgent, ok := agent.AsHookSupport(ag)
				if !ok {
					t.Fatal("Claude Code agent does not support hooks")
				}
				if _, err := hookAgent.InstallHooks(context.Background(), false); err != nil {
					t.Fatalf("install Claude Code hooks: %v", err)
				}
			},
			want: []string{"  .claude/settings.json\n"},
		},
		{
			// Deselecting an agent rewrites or removes its hook config; the
			// agent is no longer installed, but the change still has to ship.
			name: "deselected agent hook config removed",
			prepare: func(t *testing.T, dir string) {
				t.Helper()
				testutil.WriteFile(t, dir, ".cursor/hooks.json", "{}")
				testutil.GitAdd(t, dir, ".cursor/hooks.json")
				testutil.GitCommit(t, dir, "cursor hooks")
				if err := os.Remove(".cursor/hooks.json"); err != nil {
					t.Fatal(err)
				}
			},
			want: []string{"  .cursor/hooks.json\n"},
		},
		{
			name: "skill files",
			prepare: func(t *testing.T, dir string) {
				t.Helper()
				testutil.WriteFile(t, dir, agentHelpSkillTemplatePath(agent.AgentNameClaudeCode), "skill")
				testutil.WriteFile(t, dir, searchSkillTemplatePath(agent.AgentNameCodex), "skill")
			},
			want: []string{"  .claude/skills/entire/SKILL.md\n", "  .agents/skills/"},
		},
		{
			name: "everything committed",
			prepare: func(t *testing.T, dir string) {
				t.Helper()
				testutil.WriteFile(t, dir, testEntireGitignore, "tmp/\n")
				testutil.GitAdd(t, dir, EntireSettingsFile, testEntireGitignore)
				testutil.GitCommit(t, dir, "settings")
			},
			notWant: []string{"commit these files", "Session checkpoints"},
		},
		{
			name:  "empty repository",
			empty: true,
			want:  []string{"Session checkpoints require at least one commit.\nMake sure to commit these files", "  .entire/settings.json\n"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := newCommitConfigRepo(t, tt.empty)
			if tt.prepare != nil {
				tt.prepare(t, dir)
			}
			stubCommitConfig(t, failOnPrompt(t), nil)

			assertOutput(t, runOfferCommitConfig(t, EnableOptions{}), tt.want, tt.notWant)
		})
	}
}

// Not parallel: chdirs and sets environment variables.
func TestOfferCommitConfig_ListsWithoutPromptingWhenToldNotToAsk(t *testing.T) {
	tests := map[string]EnableOptions{
		"--yes":     {Yes: true},
		"agent add": {NoCommitOffer: true},
	}
	for name, opts := range tests {
		t.Run(name, func(t *testing.T) {
			newCommitConfigRepo(t, false)
			t.Setenv(interactive.EnvTestTTY, "1")
			stubCommitConfig(t, failOnPrompt(t), nil)

			assertOutput(t, runOfferCommitConfig(t, opts), []string{"Make sure to commit these files", "  .entire/settings.json\n"}, nil)
		})
	}
}

// Not parallel: chdirs and sets environment variables.
func TestOfferCommitConfig_CommitsOnlyTheConfigFiles(t *testing.T) {
	for _, empty := range []bool{false, true} {
		name := "repository with commits"
		if empty {
			name = "empty repository"
		}
		t.Run(name, func(t *testing.T) {
			dir := newCommitConfigRepo(t, empty)
			testutil.WriteFile(t, dir, "staged.txt", "unrelated")
			testutil.GitAdd(t, dir, "staged.txt")
			t.Setenv(interactive.EnvTestTTY, "1")
			branch := strings.TrimSpace(testutil.RunGit(t, dir, "symbolic-ref", "--short", "HEAD"))

			p := &promptedCommitConfig{render: &bytes.Buffer{}, commit: true}
			stubCommitConfig(t, p.prompt, nil)
			out := runOfferCommitConfig(t, EnableOptions{})

			if !slices.Equal(p.files, []string{EntireSettingsFile}) || p.branch != branch {
				t.Errorf("prompt got %v on %q, want [%s] on %q", p.files, p.branch, EntireSettingsFile, branch)
			}
			// The list and the outcome go where the prompt rendered, not to
			// the command's output.
			assertOutput(t, p.render.String(), []string{"  .entire/settings.json\n", "✓ Committed them to " + branch + "\n"}, nil)
			assertOutput(t, out, nil, []string{"Make sure to commit these files"})

			committed := strings.Fields(testutil.RunGit(t, dir, "show", "--name-only", "--format=%s", "HEAD"))
			if !slices.Equal(committed, []string{"Enable", "Entire", EntireSettingsFile}) {
				t.Errorf("HEAD = %v, want the %q commit of %s alone", committed, configCommitMessage, EntireSettingsFile)
			}
			if status := testutil.RunGit(t, dir, "status", "--porcelain", "--", "staged.txt"); !strings.HasPrefix(status, "A  staged.txt") {
				t.Errorf("staged.txt should still be staged and uncommitted, status %q", status)
			}
		})
	}
}

// Not parallel: chdirs and sets environment variables.
func TestOfferCommitConfig_DeclinedAbortedOrFailed(t *testing.T) {
	tests := []struct {
		name      string
		commit    bool
		promptErr error
		commitErr error
		render    []string
		out       []string
	}{
		{name: "declined"},
		{name: "aborted in the form", promptErr: huh.ErrUserAborted, render: []string{"Commit cancelled.\n"}},
		{
			name:      "no terminal to prompt on",
			promptErr: errors.New("open prompt terminal: no controlling terminal"),
			out:       []string{"Make sure to commit these files", "  .entire/settings.json\n"},
		},
		{
			name:      "commit failed",
			commit:    true,
			commitErr: errors.New("git commit: exit status 1"),
			render:    []string{"Could not commit them: git commit: exit status 1\n"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newCommitConfigRepo(t, false)
			t.Setenv(interactive.EnvTestTTY, "1")
			p := &promptedCommitConfig{render: &bytes.Buffer{}, commit: tt.commit, err: tt.promptErr}
			stubCommitConfig(t, p.prompt, func(context.Context, string, []string, string, io.Writer) error {
				if !tt.commit {
					t.Error("committed after the prompt was not accepted")
				}
				return tt.commitErr
			})

			out := runOfferCommitConfig(t, EnableOptions{})
			assertOutput(t, p.render.String(), tt.render, []string{"✓ Committed"})
			assertOutput(t, out, tt.out, []string{"✓ Committed"})
		})
	}
}

// Not parallel: chdirs and sets environment variables.
func TestOfferCommitConfig_InterruptIsAnError(t *testing.T) {
	newCommitConfigRepo(t, false)
	t.Setenv(interactive.EnvTestTTY, "1")
	ctx, cancel := context.WithCancel(context.Background())
	stubCommitConfig(t, func(context.Context, io.Writer, []string, string) (io.Writer, bool, error) {
		cancel() // Ctrl+C while the question is up
		return io.Discard, false, context.Canceled
	}, func(context.Context, string, []string, string, io.Writer) error {
		t.Error("committed after an interrupt")
		return nil
	})

	err := offerCommitConfig(ctx, io.Discard, EnableOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("offerCommitConfig() error = %v, want context.Canceled", err)
	}
}

// Not parallel: swaps the terminal opener and sets ACCESSIBLE.
func TestPromptCommitConfig_ListAndQuestionShareTheTerminal(t *testing.T) {
	// The command's error stream is redirected (not a terminal), so the form
	// falls back to the controlling terminal; the list must go there with it,
	// not to a stream the developer is not watching.
	t.Setenv("ACCESSIBLE", "1")
	var terminal bytes.Buffer
	prev := openPromptTerminal
	openPromptTerminal = func() (promptTerminal, error) {
		return promptTerminal{in: strings.NewReader("y\n"), out: &terminal}, nil
	}
	t.Cleanup(func() { openPromptTerminal = prev })

	var redirected bytes.Buffer
	render, commit, err := promptCommitConfig(t.Context(), &redirected, []string{EntireSettingsFile, ".claude/settings.json"}, "main")
	if err != nil {
		t.Fatalf("promptCommitConfig() error = %v", err)
	}
	if !commit {
		t.Error("answering y should commit")
	}
	if render != &terminal {
		t.Error("render should be the terminal the form used, for the outcome to follow it")
	}
	assertOutput(t, terminal.String(), []string{"  .entire/settings.json\n  .claude/settings.json\n", "Commit them to main now?"}, nil)
	if redirected.Len() != 0 {
		t.Errorf("nothing should go to the redirected stream, got:\n%s", redirected.String())
	}
}

// Not parallel: chdirs and sets environment variables.
func TestRunEnableOnConfiguredRepo_OffersPendingConfig(t *testing.T) {
	tests := []struct {
		name     string
		settings string
		want     string
	}{
		{name: "already enabled", settings: testSettingsEnabled, want: "Entire is already enabled."},
		{name: "re-enabled", settings: testSettingsDisabled, want: "Entire is now enabled."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newCommitConfigRepo(t, false)
			writeSettings(t, tt.settings)
			t.Setenv(interactive.EnvTestTTY, "1")
			p := &promptedCommitConfig{render: &bytes.Buffer{}}
			stubCommitConfig(t, p.prompt, nil)

			cmd := newEnableCmd()
			var out bytes.Buffer
			cmd.SetOut(&out)
			if err := runEnableOnConfiguredRepo(context.Background(), cmd, EnableOptions{}); err != nil {
				t.Fatalf("runEnableOnConfiguredRepo() error = %v", err)
			}
			if !p.asked || !slices.Contains(p.files, EntireSettingsFile) {
				t.Errorf("configured-repo enable should offer to commit %s; asked=%v files=%v", EntireSettingsFile, p.asked, p.files)
			}
			assertOutput(t, out.String(), []string{tt.want}, nil)
		})
	}
}
