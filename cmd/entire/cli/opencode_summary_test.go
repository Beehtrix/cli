package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/opencode"
	"github.com/entireio/cli/cmd/entire/cli/api"
	"github.com/entireio/cli/cmd/entire/cli/settings"
)

func TestConfigureOpenCodeSummaryAndAgentTuning(t *testing.T) {
	// Changes CWD and the provider registry; must remain non-parallel.
	catPath, err := exec.LookPath("cat")
	if err != nil {
		t.Fatal(err)
	}
	echoPath, err := exec.LookPath("echo")
	if err != nil {
		t.Fatal(err)
	}
	setupTestRepo(t)
	writeSettings(t, testSettingsEnabled)
	stubCLIAvailable(t)
	cmd := newSetupCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--summarize-provider", "opencode", "--summarize-model", "openai/test"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	s, err := settings.LoadFromFile(EntireSettingsFile)
	if err != nil {
		t.Fatal(err)
	}
	if s.SummaryGeneration == nil || s.SummaryGeneration.Provider != "opencode" || s.SummaryGeneration.Model != "openai/test" {
		t.Fatalf("settings = %+v", s.SummaryGeneration)
	}
	found := false
	for _, provider := range listEnabledSummaryProviders(t.Context()) {
		if provider.Name == agent.AgentNameOpenCode {
			found = true
		}
	}
	if !found {
		t.Fatal("OpenCode missing from existing provider selection")
	}
	for _, model := range []string{"", "openai/test"} {
		provider, err := buildCheckpointSummaryProvider(agent.AgentNameOpenCode, model)
		if err != nil {
			t.Fatal(err)
		}
		if provider.Model != model || provider.Generator == nil || provider.TextGenerator == nil {
			t.Fatalf("incorrect provider: %+v", provider)
		}
	}
	agents := []api.ProjectAgent{{ID: "agent-risk", ProjectAgentDefinition: api.ProjectAgentDefinition{Name: "Risk", Instructions: "Evaluate {{branch}}."}}}
	response := `{"agent-risk":"Evaluate {{branch}} and return a risk assessment."}`
	event, err := json.Marshal(map[string]any{"type": "text", "part": map[string]string{"text": response}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "events.jsonl")
	if err := os.WriteFile(path, event, 0o600); err != nil {
		t.Fatal(err)
	}
	agent.Register(agent.AgentNameOpenCode, func() agent.Agent {
		return &opencode.OpenCodeAgent{CommandRunner: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			if slices.Equal(args, []string{"--version"}) {
				return exec.CommandContext(ctx, echoPath, "1.18.33")
			}
			i := slices.Index(args, "--model")
			if i < 0 || args[i+1] != "openai/test" {
				t.Fatalf("tuning model not forwarded: %v", args)
			}
			return exec.CommandContext(ctx, catPath, path)
		}}
	})
	t.Cleanup(func() { agent.Register(agent.AgentNameOpenCode, opencode.NewOpenCodeAgent) })
	provider, err := resolveCheckpointSummaryProvider(t.Context(), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	changes, skipped, err := runTuning(t.Context(), io.Discard, provider, agents, "synthetic tuning prompt", "")
	if err != nil {
		t.Fatal(err)
	}
	if skipped != 0 || len(changes) != 1 {
		t.Fatalf("changes = %+v, skipped = %d", changes, skipped)
	}
	if changes[0].instructions != "Evaluate {{branch}} and return a risk assessment." {
		t.Fatalf("instructions = %q", changes[0].instructions)
	}
}
