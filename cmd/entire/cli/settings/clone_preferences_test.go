package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

func clonePreferencesTestPath(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	testutil.InitRepo(t, root)
	path, err := clonePreferencesPathForWorktreeRoot(t.Context(), root)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	return path
}

func writeClonePreferencesTestFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func readClonePreferencesTestKeys(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var keys map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &keys))
	return keys
}

// Several writers rewrite this file without the user doing anything (the
// trails cache refreshes on SessionStart), so a key one binary does not know
// must survive another binary's rewrite. A dropped checkpoint_push_policy
// would read as automatic.
func TestModifyClonePreferences_KeepsUnknownKeys(t *testing.T) {
	t.Parallel()
	path := clonePreferencesTestPath(t)
	writeClonePreferencesTestFile(t, path, `{"review_fix_agent":"codex","future_key":{"nested":[1,2]},"future_flag":true}`)

	require.NoError(t, modifyClonePreferencesFile(path, func(p *ClonePreferences) error {
		p.ReviewFixAgent = "claude-code"
		return nil
	}))

	keys := readClonePreferencesTestKeys(t, path)
	assert.JSONEq(t, `"claude-code"`, string(keys["review_fix_agent"]))
	assert.JSONEq(t, `{"nested":[1,2]}`, string(keys["future_key"]))
	assert.JSONEq(t, `true`, string(keys["future_flag"]))
}

// encoding/json matches field names case-insensitively, so decoding the whole
// object would let a case variant set a field and then also be written back as
// an unknown key.
func TestModifyClonePreferences_CaseVariantOfKnownKeyStaysUnknown(t *testing.T) {
	t.Parallel()
	path := clonePreferencesTestPath(t)
	writeClonePreferencesTestFile(t, path, `{"Review_Fix_Agent":"codex"}`)

	prefs, err := loadClonePreferencesFromFile(path)
	require.NoError(t, err)
	assert.Empty(t, prefs.ReviewFixAgent, "only the exact key sets a field")

	require.NoError(t, modifyClonePreferencesFile(path, func(p *ClonePreferences) error {
		p.ReviewFixAgent = "claude-code"
		return nil
	}))

	keys := readClonePreferencesTestKeys(t, path)
	assert.JSONEq(t, `"claude-code"`, string(keys["review_fix_agent"]))
	assert.JSONEq(t, `"codex"`, string(keys["Review_Fix_Agent"]))
}

func TestModifyClonePreferences_ClearedKnownKeyIsRemoved(t *testing.T) {
	t.Parallel()
	path := clonePreferencesTestPath(t)
	writeClonePreferencesTestFile(t, path, `{"review_fix_agent":"codex","future_key":1}`)

	require.NoError(t, modifyClonePreferencesFile(path, func(p *ClonePreferences) error {
		p.ReviewFixAgent = ""
		return nil
	}))

	keys := readClonePreferencesTestKeys(t, path)
	assert.NotContains(t, keys, "review_fix_agent", "a cleared field must not come back from the preserved keys")
	assert.JSONEq(t, `1`, string(keys["future_key"]))
}

func TestModifyClonePreferences_MalformedFileIsNotOverwritten(t *testing.T) {
	t.Parallel()
	path := clonePreferencesTestPath(t)
	writeClonePreferencesTestFile(t, path, `{not json`)

	err := modifyClonePreferencesFile(path, func(p *ClonePreferences) error {
		p.ReviewFixAgent = "claude-code"
		return nil
	})
	require.Error(t, err)

	data, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, `{not json`, string(data))
}

func TestClonePreferences_EffectiveCheckpointPushPolicy(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
		want    CheckpointPushPolicy
		wantErr string
	}{
		{name: "missing key is automatic", content: `{}`, want: CheckpointPushPolicyAutomatic},
		{name: "automatic", content: `{"checkpoint_push_policy":"automatic"}`, want: CheckpointPushPolicyAutomatic},
		{name: "manual", content: `{"checkpoint_push_policy":"manual"}`, want: CheckpointPushPolicyManual},
		{name: "unknown value", content: `{"checkpoint_push_policy":"off"}`, wantErr: `"off"`},
		{name: "wrong case", content: `{"checkpoint_push_policy":"Manual"}`, wantErr: `"Manual"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := clonePreferencesTestPath(t)
			writeClonePreferencesTestFile(t, path, tt.content)

			prefs, err := loadClonePreferencesFromFile(path)
			require.NoError(t, err)
			got, err := prefs.EffectiveCheckpointPushPolicy()
			if tt.wantErr != "" {
				require.Error(t, err, "an unrecognized policy must not read as automatic")
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestClonePreferences_MissingFileIsAutomatic(t *testing.T) {
	t.Parallel()
	path := clonePreferencesTestPath(t)

	prefs, err := loadClonePreferencesFromFile(path)
	require.NoError(t, err)
	got, err := prefs.EffectiveCheckpointPushPolicy()
	require.NoError(t, err)
	assert.Equal(t, CheckpointPushPolicyAutomatic, got)
}

func TestClonePreferences_CheckpointPushPolicyWrongTypeFailsLoad(t *testing.T) {
	t.Parallel()
	path := clonePreferencesTestPath(t)
	writeClonePreferencesTestFile(t, path, `{"checkpoint_push_policy":1}`)

	_, err := loadClonePreferencesFromFile(path)
	require.Error(t, err)
}

// A case variant of the policy key is kept as unknown, so reading past it
// would turn a stored manual into automatic. The policy read fails closed.
func TestLoadCheckpointPushPolicy_CaseVariantKeyIsUnreadable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		content string
	}{
		{name: "variant alone", content: `{"Checkpoint_Push_Policy":"manual"}`},
		{name: "variant beside the exact key", content: `{"checkpoint_push_policy":"automatic","CHECKPOINT_PUSH_POLICY":"manual"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := clonePreferencesTestPath(t)
			writeClonePreferencesTestFile(t, path, tt.content)

			_, err := loadCheckpointPushPolicyFromFile(path)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "checkpoint_push_policy")
		})
	}
}

func TestModifyClonePreferences_CheckpointPushPolicySurvivesUnrelatedWrites(t *testing.T) {
	t.Parallel()
	path := clonePreferencesTestPath(t)

	require.NoError(t, modifyClonePreferencesFile(path, func(p *ClonePreferences) error {
		p.CheckpointPushPolicy = CheckpointPushPolicyManual
		return nil
	}))
	enabled := true
	require.NoError(t, modifyClonePreferencesFile(path, func(p *ClonePreferences) error {
		p.TrailsEnabled = &enabled
		return nil
	}))

	prefs, err := loadClonePreferencesFromFile(path)
	require.NoError(t, err)
	got, err := prefs.EffectiveCheckpointPushPolicy()
	require.NoError(t, err)
	assert.Equal(t, CheckpointPushPolicyManual, got)
}

func TestParseCheckpointPushPolicy(t *testing.T) {
	t.Parallel()
	for _, valid := range []CheckpointPushPolicy{CheckpointPushPolicyAutomatic, CheckpointPushPolicyManual} {
		got, err := ParseCheckpointPushPolicy(string(valid))
		require.NoError(t, err)
		assert.Equal(t, valid, got)
	}
	for _, invalid := range []string{"", "Manual", "all", "approved"} {
		_, err := ParseCheckpointPushPolicy(invalid)
		require.Error(t, err, "%q must be rejected", invalid)
	}
}
