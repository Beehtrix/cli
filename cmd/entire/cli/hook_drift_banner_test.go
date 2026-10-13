package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/claudecode"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/stretchr/testify/require"
)

// staleClaudeHookSettings is a Claude Code config an older CLI wrote: Entire is
// installed (Stop hook present) but the tool-use hooks sit under the
// non-firing Task/TodoWrite matchers, so CheckHookConfig reads it as outdated.
const staleClaudeHookSettings = `{
  "hooks": {
    "Stop": [{"matcher": "", "hooks": [{"type": "command", "command": "entire hooks claude-code stop"}]}],
    "PreToolUse": [{"matcher": "Task", "hooks": [{"type": "command", "command": "entire hooks claude-code pre-task"}]}],
    "PostToolUse": [
      {"matcher": "Task", "hooks": [{"type": "command", "command": "entire hooks claude-code post-task"}]},
      {"matcher": "TodoWrite", "hooks": [{"type": "command", "command": "entire hooks claude-code post-todo"}]}
    ]
  }
}`

func writeStaleClaudeHookSettings(t *testing.T, repoDir string) {
	t.Helper()
	claudeDir := filepath.Join(repoDir, claudeDirName)
	require.NoError(t, os.MkdirAll(claudeDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, claudecode.ClaudeSettingsFileName), []byte(staleClaudeHookSettings), 0o600))
}

// The remedy names the registry name, which is what `entire agent add` takes,
// not the display name.
func TestRefreshAgentHooksCommand(t *testing.T) {
	t.Parallel()

	for name, want := range map[types.AgentName]string{
		agent.AgentNameClaudeCode: "entire agent add claude-code --force",
		agent.AgentNameCodex:      "entire agent add codex --force",
		agent.AgentNameOpenCode:   "entire agent add opencode --force",
	} {
		require.Equal(t, want, refreshAgentHooksCommand(name))
	}
}

func mustGetAgent(t *testing.T, name types.AgentName) agent.Agent {
	t.Helper()
	ag, err := agent.Get(name)
	require.NoError(t, err)
	return ag
}

// The rest change directory (t.Chdir), so they cannot run in parallel.

func TestOutdatedHooksBannerSuffix_ClaudeCodeOutdated(t *testing.T) {
	dir := setupGitRepoForPhaseTest(t)
	t.Chdir(dir)
	writeStaleClaudeHookSettings(t, dir)

	got := outdatedHooksBannerSuffix(context.Background(), mustGetAgent(t, agent.AgentNameClaudeCode))
	require.Contains(t, got, "Entire's Claude Code hooks in this repo are out of date")
	require.Contains(t, got, "Run 'entire agent add claude-code --force' to update them.")
}

func TestOutdatedHooksBannerSuffix_ClaudeCodeCurrent(t *testing.T) {
	dir := setupGitRepoForPhaseTest(t)
	t.Chdir(dir)
	ag := mustGetAgent(t, agent.AgentNameClaudeCode)
	hs, ok := agent.AsHookSupport(ag)
	require.True(t, ok)
	_, err := hs.InstallHooks(context.Background(), false)
	require.NoError(t, err)

	require.Empty(t, outdatedHooksBannerSuffix(context.Background(), ag))
}

func TestOutdatedHooksBannerSuffix_ClaudeCodeAbsent(t *testing.T) {
	t.Chdir(setupGitRepoForPhaseTest(t))

	require.Empty(t, outdatedHooksBannerSuffix(context.Background(), mustGetAgent(t, agent.AgentNameClaudeCode)))
}

// OpenCode's check compares the whole generated plugin, so a teammate on a
// different CLI version reads as outdated. It must not reach the banner.
func TestOutdatedHooksBannerSuffix_OpenCodeOutdatedStaysSilent(t *testing.T) {
	dir := setupGitRepoForPhaseTest(t)
	t.Chdir(dir)
	ctx := context.Background()
	ag := mustGetAgent(t, agent.AgentNameOpenCode)
	hs, ok := agent.AsHookSupport(ag)
	require.True(t, ok)
	_, err := hs.InstallHooks(ctx, false)
	require.NoError(t, err)

	plugin := filepath.Join(dir, ".opencode", "plugins", "entire.ts")
	data, err := os.ReadFile(plugin)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(plugin, append(data, "// edited by another CLI version\n"...), 0o600))
	hf, ok := agent.AsHookFreshness(ag)
	require.True(t, ok)
	require.Equal(t, agent.HooksOutdated, hf.CheckHookConfig(ctx), "fixture must read as outdated")

	require.Empty(t, outdatedHooksBannerSuffix(ctx, ag))
}
