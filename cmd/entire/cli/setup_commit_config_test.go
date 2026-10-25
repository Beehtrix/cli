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

// stubCommitConfig replaces the prompt and the commit for one test.
func stubCommitConfig(t *testing.T, prompt func(string) (bool, error), commit func(context.Context, io.Writer, string, []string) error) {
	t.Helper()
	origPrompt, origCommit := commitConfigPrompt, commitConfigFiles
	t.Cleanup(func() { commitConfigPrompt, commitConfigFiles = origPrompt, origCommit })
	if prompt != nil {
		commitConfigPrompt = prompt
	}
	if commit != nil {
		commitConfigFiles = commit
	}
}

func failOnPrompt(t *testing.T) func(string) (bool, error) {
	t.Helper()
	return func(string) (bool, error) {
		t.Error("prompted to commit when no prompt was expected")
		return false, nil
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

// Not parallel: newCommitConfigRepo chdirs and sets environment variables.
func TestOfferCommitConfig_ListsPendingFilesWithoutPrompting(t *testing.T) {
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

			var out bytes.Buffer
			offerCommitConfig(context.Background(), &out, EnableOptions{})
			assertOutput(t, out.String(), tt.want, tt.notWant)
		})
	}
}

// Not parallel: chdirs and sets environment variables.
func TestOfferCommitConfig_YesFlagNeverPrompts(t *testing.T) {
	newCommitConfigRepo(t, false)
	t.Setenv(interactive.EnvTestTTY, "1")
	stubCommitConfig(t, failOnPrompt(t), nil)

	var out bytes.Buffer
	offerCommitConfig(context.Background(), &out, EnableOptions{Yes: true})
	assertOutput(t, out.String(), []string{"Make sure to commit these files", "  .entire/settings.json\n"}, nil)
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

			var askedBranch string
			stubCommitConfig(t, func(b string) (bool, error) {
				askedBranch = b
				return true, nil
			}, nil)

			var out bytes.Buffer
			offerCommitConfig(context.Background(), &out, EnableOptions{})

			if askedBranch != branch {
				t.Errorf("prompt got branch %q, want %q", askedBranch, branch)
			}
			assertOutput(t, out.String(), []string{"  .entire/settings.json\n", "✓ Committed them to " + branch + "\n"}, nil)

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
func TestOfferCommitConfig_DeclinedOrFailedCommitListsFiles(t *testing.T) {
	tests := []struct {
		name   string
		prompt func(string) (bool, error)
		commit func(context.Context, io.Writer, string, []string) error
		want   []string
	}{
		{
			name:   "declined",
			prompt: func(string) (bool, error) { return false, nil },
		},
		{
			name:   "prompt aborted",
			prompt: func(string) (bool, error) { return false, errors.New("user aborted") },
		},
		{
			name:   "commit failed",
			prompt: func(string) (bool, error) { return true, nil },
			commit: func(context.Context, io.Writer, string, []string) error {
				return errors.New("git commit: exit status 1")
			},
			want: []string{"Could not commit them: git commit: exit status 1\n"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newCommitConfigRepo(t, false)
			t.Setenv(interactive.EnvTestTTY, "1")
			commit := tt.commit
			if commit == nil {
				commit = func(context.Context, io.Writer, string, []string) error {
					t.Error("committed after the prompt was not accepted")
					return nil
				}
			}
			stubCommitConfig(t, tt.prompt, commit)

			var out bytes.Buffer
			offerCommitConfig(context.Background(), &out, EnableOptions{})
			want := append(slices.Clone(tt.want), "Make sure to commit these files", "  .entire/settings.json\n")
			assertOutput(t, out.String(), want, []string{"✓ Committed"})
		})
	}
}
