package review

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	reviewtypes "github.com/entireio/cli/cmd/entire/cli/review/types"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

func TestValidateAgentConfig(t *testing.T) {
	t.Parallel()

	mcp := func(def string) map[string]json.RawMessage {
		return map[string]json.RawMessage{"docs": json.RawMessage(def)}
	}
	tests := []struct {
		name    string
		agent   string
		cfg     reviewtypes.AgentConfig
		wantErr string
	}{
		// Commands are the user's own and are not vetted, launchers included.
		{"claude hook with a launcher", "claude-code", reviewtypes.AgentConfig{Settings: json.RawMessage(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"yarn lint"}]}]}}`)}, ""},
		{"MCP command", "codex", reviewtypes.AgentConfig{MCPServers: mcp(`{"command":"npx","args":["server"]}`)}, ""},
		{"MCP url", "claude-code", reviewtypes.AgentConfig{MCPServers: mcp(`{"url":"https://mcp.example"}`)}, ""},
		{"MCP without command or url", "claude-code", reviewtypes.AgentConfig{MCPServers: mcp(`{"args":["x"]}`)}, "needs a command or a url"},
		{"MCP bad name", "codex", reviewtypes.AgentConfig{MCPServers: map[string]json.RawMessage{"a.b": json.RawMessage(`{"url":"x"}`)}}, "letters, digits"},
		{"settings not an object", "claude-code", reviewtypes.AgentConfig{Settings: json.RawMessage(`[]`)}, "settings"},
		{"codex settings unsupported", "codex", reviewtypes.AgentConfig{Settings: json.RawMessage(`{}`)}, `does not support "settings"`},
		{"pi MCP unsupported", "pi", reviewtypes.AgentConfig{MCPServers: mcp(`{"url":"x"}`)}, `does not support "mcp_servers"`},
		{"pi extension", "pi", reviewtypes.AgentConfig{Extensions: []string{"/opt/ext.ts"}}, ""},
		{"unknown agent", "cursor", reviewtypes.AgentConfig{}, "does not support a review profile config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := tt.cfg
			err := ValidateAgentConfig(tt.agent, &cfg)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateAgentConfig() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("ValidateAgentConfig() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

// Values naming a path inside the checkout are warned about, not refused.
func TestAgentConfigWarnings(t *testing.T) {
	t.Parallel()

	roots := []string{"/repo"}
	tests := []struct {
		name string
		cfg  reviewtypes.AgentConfig
		want int
	}{
		{"hook in checkout", reviewtypes.AgentConfig{Settings: json.RawMessage(`{"hooks":{"Stop":[{"hooks":[{"command":"/repo/bin/hook --x"}]}]}}`)}, 1},
		{"project dir variable", reviewtypes.AgentConfig{Settings: json.RawMessage(`{"statusLine":{"command":"$CLAUDE_PROJECT_DIR/s.sh"}}`)}, 1},
		{"MCP env PATH into checkout", reviewtypes.AgentConfig{MCPServers: map[string]json.RawMessage{"x": json.RawMessage(`{"command":"tool","env":{"PATH":"/usr/bin:/repo/bin"}}`)}}, 1},
		{"flag value in checkout", reviewtypes.AgentConfig{Settings: json.RawMessage(`{"apiKeyHelper":"/opt/key --config=/repo/k.json"}`)}, 1},
		{"extension in checkout", reviewtypes.AgentConfig{Extensions: []string{"/repo/.pi/x.ts"}}, 1},
		{"similar prefix", reviewtypes.AgentConfig{Extensions: []string{"/repository/x.ts"}}, 0},
		{"outside", reviewtypes.AgentConfig{Settings: json.RawMessage(`{"hooks":{"Stop":[{"hooks":[{"command":"yarn lint"}]}]}}`)}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg := tt.cfg
			if got := AgentConfigWarnings(&cfg, roots); len(got) != tt.want {
				t.Fatalf("AgentConfigWarnings() = %q, want %d", got, tt.want)
			}
		})
	}
}

// A path outside the checkout that links into it is inside it.
func TestAgentConfigWarningsFollowSymlinks(t *testing.T) {
	t.Parallel()

	checkout := t.TempDir()
	testutil.WriteFile(t, checkout, ".pi/ext.ts", "x")
	link := filepath.Join(t.TempDir(), "ext.ts")
	if err := os.Symlink(filepath.Join(checkout, ".pi", "ext.ts"), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(checkout)
	if err != nil {
		t.Fatal(err)
	}
	if got := AgentConfigWarnings(&reviewtypes.AgentConfig{Extensions: []string{link}}, []string{resolved}); len(got) != 1 {
		t.Fatalf("AgentConfigWarnings() = %q, want one warning", got)
	}
}

// An agent counts as isolated only when every worker on it has a profile
// config, so a mixed profile is never under-reported by the gate.
func TestProfileTrustAgents(t *testing.T) {
	t.Parallel()

	profile := settings.ReviewProfileConfig{Agents: map[string]settings.ReviewConfig{
		"claude-a": {Agent: "claude-code", Config: &settings.ReviewAgentConfig{}},
		"claude-b": {Agent: "claude-code"},
		"pi":       {Config: &settings.ReviewAgentConfig{}},
	}}
	got := profileTrustAgents(profile, "")
	want := map[string]bool{"claude-code": false, "pi": true}
	if len(got) != len(want) {
		t.Fatalf("profileTrustAgents() = %+v", got)
	}
	for _, a := range got {
		if want[a.Name] != a.Isolated {
			t.Errorf("%s isolated = %v, want %v", a.Name, a.Isolated, want[a.Name])
		}
	}
	if only := profileTrustAgents(profile, "claude-a"); len(only) != 1 || !only[0].Isolated {
		t.Errorf("--agent claude-a = %+v, want one isolated agent", only)
	}
}

func TestTrustConfirmTextForProfileConfig(t *testing.T) {
	t.Parallel()

	subject := foreignSubject()
	inv := TrustInventory{Isolated: true, Entries: []TrustEntry{
		{Agent: "claude-code", Kind: TrustKindSkill, Name: "review", Command: ".claude/skills/review", Source: ".claude/skills"},
	}}
	title, body := trustConfirmText(subject, inv, "x")
	if title != "Load this branch's skills during the review?" || !strings.Contains(body, "uses your profile's config") {
		t.Fatalf("title/body = %q / %q", title, body)
	}
	if title, body := trustConfirmText(subject, TrustInventory{Isolated: true}, "x"); title != "Review this branch?" || !strings.Contains(body, "uses your profile's config") || strings.Contains(body, "nothing from it runs") {
		t.Fatalf("empty isolated title/body = %q / %q", title, body)
	}
}
