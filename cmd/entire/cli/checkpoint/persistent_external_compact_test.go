package checkpoint

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/external"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/redact"
)

// installCompactingExternalAgent puts a mock external agent on $PATH that
// declares compact_transcript and registers it. Its compact-transcript emits
// one unified line per input line ("agent-compacted N"), so a test can tell its
// output from the built-in compactor's and check prefix-based scoping. mode
// "fail" makes compact-transcript exit non-zero; "assets" adds an asset;
// "secret" embeds a credential in the output.
func installCompactingExternalAgent(t *testing.T, name, agentType, mode string) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh not available")
	}
	t.Cleanup(agent.SnapshotRegistryForTesting())

	extra := ""
	if mode == "assets" {
		extra = `,"assets":[{"name":"img-001.png","media_type":"image/png","data":"aGk="}]`
	}
	text := `agent-compacted $i`
	if mode == "secret" {
		text = `agent-compacted $i sk-ant-api03-xK9mZ2vL8nQ5rT1wY4bC7dF0gH3jE6pA`
	}
	script := `#!/bin/sh
case "$1" in
  info)
    echo '{"protocol_version":1,"name":"` + name + `","type":"` + agentType + `","description":"compact test agent","protected_dirs":[],"hook_names":[],"capabilities":{"compact_transcript":true}}'
    ;;
  compact-transcript)
    [ "` + mode + `" = "fail" ] && { echo "compactor exploded" >&2; exit 1; }
    n=$(grep -c '' "$3")
    i=1
    out=""
    while [ "$i" -le "$n" ]; do
      out="$out{\"v\":1,\"agent\":\"` + name + `\",\"type\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"` + text + `\"}]}
"
      i=$((i+1))
    done
    printf '{"transcript":"%s"` + extra + `}' "$(printf '%s' "$out" | base64 | tr -d '\n')"
    ;;
esac
`
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "entire-agent-"+name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := external.DiscoverAndRegisterNamedAlways(context.Background(), types.AgentName(name)); err != nil {
		t.Fatalf("register mock external agent: %v", err)
	}
}

func writeExternalAgentCheckpoint(t *testing.T, agentType types.AgentType, cpHex string, start int) (*GitStore, string) {
	t.Helper()
	repo := setupTestRepo(t)
	store := NewGitStore(repo, DefaultV1Refs())
	cpID := id.MustCheckpointID(cpHex)
	err := store.Write(context.Background(), Session{
		CheckpointID:              cpID,
		SessionID:                 "session-ext",
		Strategy:                  "manual-commit",
		Transcript:                redact.AlreadyRedacted(claudeStyleTranscript()),
		Agent:                     agentType,
		CheckpointTranscriptStart: start,
		AuthorName:                "Test",
		AuthorEmail:               "test@test.com",
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	return store, cpID.Path() + "/0/"
}

// TestWriteCommitted_ExternalAgentCompactsTranscript pins that an external agent
// declaring compact_transcript writes transcript.jsonl with its own compactor:
// the built-in one only knows built-in agents' native formats and produces empty
// lines for anything else.
func TestWriteCommitted_ExternalAgentCompactsTranscript(t *testing.T) {
	// Cannot use t.Parallel: mutates $PATH and the agent registry.
	installCompactingExternalAgent(t, "compactok", "Compact OK", "")
	store, sessionPath := writeExternalAgentCheckpoint(t, "Compact OK", "d4e5f6a1b2c3", 2)

	compact, ok := readBranchFile(t, store, sessionPath+paths.CompactTranscriptFileName)
	if !ok {
		t.Fatal("transcript.jsonl missing from checkpoint tree")
	}
	if !strings.Contains(compact, "agent-compacted 4") || strings.Contains(compact, "reply two") {
		t.Fatalf("transcript.jsonl was not produced by the agent's compactor:\n%s", compact)
	}

	// Scoping: the checkpoint starts at raw line 2, and the agent compacts the
	// first two raw lines into two lines, so this checkpoint's slice starts at 2.
	meta := readSessionMetadata(t, store.repo, id.MustCheckpointID("d4e5f6a1b2c3"))
	marker, ok := meta.GetCompactTranscriptStart()
	if !ok || marker != 2 {
		t.Fatalf("compact_transcript_start = %d (set=%v), want 2", marker, ok)
	}
}

// TestWriteCommitted_ExternalCompactorFailureFallsBack pins that a failing
// agent compactor never costs the checkpoint its compact transcript.
func TestWriteCommitted_ExternalCompactorFailureFallsBack(t *testing.T) {
	// Cannot use t.Parallel: mutates $PATH and the agent registry.
	installCompactingExternalAgent(t, "compactfail", "Compact Fail", "fail")
	store, sessionPath := writeExternalAgentCheckpoint(t, "Compact Fail", "e5f6a1b2c3d4", 0)

	compact, ok := readBranchFile(t, store, sessionPath+paths.CompactTranscriptFileName)
	if !ok || !strings.Contains(compact, "reply two") {
		t.Fatalf("expected the built-in compact transcript after the agent compactor failed, got (present=%v):\n%s", ok, compact)
	}
}

// TestWriteCommitted_ExternalCompactorAssetsFallBack pins that a compaction with
// binary assets falls back: checkpoints have nowhere to store agent-extracted
// assets yet, and transcript lines would point at missing files.
func TestWriteCommitted_ExternalCompactorAssetsFallBack(t *testing.T) {
	// Cannot use t.Parallel: mutates $PATH and the agent registry.
	installCompactingExternalAgent(t, "compactassets", "Compact Assets", "assets")
	store, sessionPath := writeExternalAgentCheckpoint(t, "Compact Assets", "f6a1b2c3d4e5", 0)

	compact, ok := readBranchFile(t, store, sessionPath+paths.CompactTranscriptFileName)
	if !ok || !strings.Contains(compact, "reply two") || strings.Contains(compact, "agent-compacted") {
		t.Fatalf("expected the built-in compact transcript when the agent returned assets, got (present=%v):\n%s", ok, compact)
	}
}

// TestWriteCommitted_ExternalCompactorOutputIsRedacted pins that the agent's
// output is redacted before it is stored: the plugin is outside the redaction
// pipeline, so its output is treated as untrusted.
func TestWriteCommitted_ExternalCompactorOutputIsRedacted(t *testing.T) {
	// Cannot use t.Parallel: mutates $PATH and the agent registry.
	installCompactingExternalAgent(t, "compactsecret", "Compact Secret", "secret")
	store, sessionPath := writeExternalAgentCheckpoint(t, "Compact Secret", "a1b2c3d4e5f7", 0)

	compact, ok := readBranchFile(t, store, sessionPath+paths.CompactTranscriptFileName)
	if !ok {
		t.Fatal("transcript.jsonl missing from checkpoint tree")
	}
	if strings.Contains(compact, "sk-ant-api03-xK9mZ2vL8nQ5rT1wY4bC7dF0gH3jE6pA") {
		t.Fatalf("agent-compacted transcript stored an unredacted credential:\n%s", compact)
	}
	if !strings.Contains(compact, "agent-compacted") {
		t.Fatalf("expected the agent's (redacted) output, got:\n%s", compact)
	}
}
