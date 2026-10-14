package antigravity

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeHooksJSON(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, ".agents", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// An install from before the Stop timeout leaves agy's 30s default in force,
// shorter than the turn-end budget derived from stopHookTimeoutSeconds.
func TestRepairHookTimeouts_AddsMissingStopTimeout(t *testing.T) {
	tmpDir := t.TempDir()
	t.Chdir(tmpDir)
	stopCmd := buildEntireHookConfig().Stop[0].Command
	path := writeHooksJSON(t, tmpDir, fmt.Sprintf(`{
  "mine": {"Stop": [{"type": "command", "command": "my-hook"}]},
  "entire": {
    "futureKey": true,
    "Stop": [{"type": "command", "command": %q, "futureHandlerKey": 1}]
  }
}`, stopCmd))

	changed, err := (&AntigravityAgent{}).RepairHookTimeouts(context.Background())
	if err != nil {
		t.Fatalf("RepairHookTimeouts: %v", err)
	}
	if !changed {
		t.Fatal("changed = false, want true")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]map[string]json.RawMessage
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	var stop []map[string]any
	if err := json.Unmarshal(file["entire"]["Stop"], &stop); err != nil {
		t.Fatal(err)
	}
	if got := stop[0]["timeout"]; got != float64(stopHookTimeoutSeconds) {
		t.Errorf("Stop timeout = %v, want %d", got, stopHookTimeoutSeconds)
	}
	if stop[0]["futureHandlerKey"] != float64(1) || string(file["entire"]["futureKey"]) != "true" {
		t.Errorf("unmodeled keys in the entire entry were dropped: %s", data)
	}
	if _, ok := file["mine"]["Stop"]; !ok {
		t.Errorf("foreign entry was dropped: %s", data)
	}
	var mine []map[string]any
	if err := json.Unmarshal(file["mine"]["Stop"], &mine); err != nil {
		t.Fatal(err)
	}
	if _, ok := mine[0]["timeout"]; ok {
		t.Errorf("foreign Stop handler gained a timeout: %s", data)
	}
}

func TestRepairHookTimeouts_LeavesFileAloneWhenNothingToAdd(t *testing.T) {
	stopCmd := buildEntireHookConfig().Stop[0].Command
	for _, tc := range []struct {
		name    string
		content string
	}{
		{name: "no_file"},
		{name: "no_entire_entry", content: `{"mine": {"Stop": [{"type": "command", "command": "my-hook"}]}}`},
		{name: "own_timeout_kept", content: fmt.Sprintf(`{"entire": {"Stop": [{"type": "command", "command": %q, "timeout": 45}]}}`, stopCmd)},
		{name: "unparseable_entry", content: `{"entire": {"Stop": "not a handler list"}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Chdir(tmpDir)
			path := filepath.Join(tmpDir, ".agents", "hooks.json")
			if tc.content != "" {
				writeHooksJSON(t, tmpDir, tc.content)
			}

			changed, err := (&AntigravityAgent{}).RepairHookTimeouts(context.Background())
			if err != nil {
				t.Fatalf("RepairHookTimeouts: %v", err)
			}
			if changed {
				t.Error("changed = true, want false")
			}
			data, err := os.ReadFile(path)
			if tc.content == "" {
				if !os.IsNotExist(err) {
					t.Errorf("repair created hooks.json: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(data) != tc.content {
				t.Errorf("hooks.json rewritten:\n%s", data)
			}
		})
	}
}
