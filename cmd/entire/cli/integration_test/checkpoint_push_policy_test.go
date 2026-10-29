//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/execx"
	"github.com/entireio/cli/cmd/entire/cli/settings"
	"github.com/entireio/cli/cmd/entire/cli/testutil"
	"github.com/stretchr/testify/require"
)

// checkpointPushJSON decodes `entire checkpoint push --json`.
type checkpointPushJSON struct {
	Results []checkpointPushResultJSON `json:"results"`
}

type checkpointPushResultJSON struct {
	CheckpointID    string `json:"checkpoint_id"`
	Ref             string `json:"ref"`
	Commit          string `json:"commit"`
	RequestedCommit string `json:"requested_commit"`
	Status          string `json:"status"`
	Error           string `json:"error"`
}

func (r checkpointPushJSON) status(t *testing.T, checkpointID string) string {
	t.Helper()
	for _, result := range r.Results {
		if result.CheckpointID == checkpointID {
			return result.Status
		}
	}
	t.Fatalf("checkpoint %s not in checkpoint push results: %+v", checkpointID, r.Results)
	return ""
}

// policyListing is the part of `checkpoint list --outgoing --json` the policy adds.
type policyListing struct {
	Policy      string `json:"checkpoint_push_policy"`
	PolicyError string `json:"checkpoint_push_policy_error"`
	Checkpoints []struct {
		CheckpointID string   `json:"checkpoint_id"`
		Ref          string   `json:"ref"`
		Commit       string   `json:"commit"`
		SessionIDs   []string `json:"session_ids"`
	} `json:"checkpoints"`
}

func listPolicyOutgoing(t *testing.T, env *TestEnv) policyListing {
	t.Helper()
	var listing policyListing
	raw := cliStdout(t, env, "checkpoint", "list", "--outgoing", "--json")
	require.NoError(t, json.Unmarshal([]byte(raw), &listing), "outgoing --json must be valid JSON, got: %s", raw)
	return listing
}

// outgoingCommit is the pin an `<id>@<commit>` caller copies from --outgoing.
func outgoingCommit(t *testing.T, env *TestEnv, checkpointID string) string {
	t.Helper()
	for _, row := range listPolicyOutgoing(t, env).Checkpoints {
		if row.CheckpointID == checkpointID {
			require.NotEmpty(t, row.Commit, "--outgoing must give the commit to pin")
			return row.Commit
		}
	}
	t.Fatalf("checkpoint %s is not outgoing", checkpointID)
	return ""
}

func gitCommonDirFile(t *testing.T, env *TestEnv, name string) string {
	t.Helper()
	commonDir := strings.TrimSpace(testutil.RunGit(t, env.RepoDir, "rev-parse", "--path-format=absolute", "--git-common-dir"))
	return filepath.Join(commonDir, filepath.FromSlash(name))
}

func writeGitCommonDirFile(t *testing.T, env *TestEnv, name, content string) {
	t.Helper()
	path := gitCommonDirFile(t, env, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func setCheckpointPushPolicy(t *testing.T, env *TestEnv, policy string) {
	t.Helper()
	writeGitCommonDirFile(t, env, settings.CheckpointPushPolicyFile, policy+"\n")
}

// gitPushWithHooksIn pushes HEAD from dir through the real pre-push hook and
// returns git's combined output, which carries the hook's stderr.
func gitPushWithHooksIn(t *testing.T, env *TestEnv, dir string) string {
	t.Helper()
	env.InstallRealPrePushHook()
	cmd := execx.NonInteractive(t.Context(), "git", "push", "origin", "HEAD")
	cmd.Dir = dir
	cmd.Env = env.cliEnv()
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "the code push must succeed whatever the policy: %s", output)
	return string(output)
}

// runCheckpointPush runs `entire checkpoint push`, as a terminal when tty is
// true, and returns stdout, stderr and the exit error.
func runCheckpointPush(t *testing.T, env *TestEnv, tty bool, args ...string) (string, string, error) {
	t.Helper()
	ttyValue := "0"
	if tty {
		ttyValue = "1"
	}
	cmd := execx.NonInteractive(t.Context(), getTestBinary(), append([]string{"checkpoint", "push"}, args...)...)
	cmd.Dir = env.RepoDir
	cmd.Env = append(env.cliEnv(), "ENTIRE_TEST_TTY="+ttyValue)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err
}

func runCheckpointPushJSON(t *testing.T, env *TestEnv, tty bool, args ...string) (checkpointPushJSON, error) {
	t.Helper()
	stdout, stderr, err := runCheckpointPush(t, env, tty, append(args, "--json")...)
	var parsed checkpointPushJSON
	require.NoError(t, json.Unmarshal([]byte(stdout), &parsed), "checkpoint push --json must be valid JSON\nstdout: %s\nstderr: %s", stdout, stderr)
	return parsed, err
}

func remoteBranchMatchesHead(t *testing.T, env *TestEnv, bareDir string) {
	t.Helper()
	branch := strings.TrimSpace(testutil.RunGit(t, env.RepoDir, "rev-parse", "--abbrev-ref", "HEAD"))
	head := strings.TrimSpace(testutil.RunGit(t, env.RepoDir, "rev-parse", "HEAD"))
	remote := strings.TrimSpace(testutil.RunGit(t, bareDir, "rev-parse", "refs/heads/"+branch))
	require.Equal(t, head, remote, "the user's code must reach the remote")
}

func TestCheckpointPushPolicy_ManualGitPushSendsNoCheckpoints(t *testing.T) {
	t.Parallel()
	env := newOutgoingEnv(t)
	bareDir := env.SetupBareRemote()
	setCheckpointPushPolicy(t, env, "manual")

	auth := createCheckpointedCommit(t, env, "Add auth module", "auth.go", "package auth", "Add auth module")
	billing := createCheckpointedCommit(t, env, "Add billing module", "billing.go", "package billing", "Add billing module")

	output := gitPushWithHooksIn(t, env, env.RepoDir)

	remoteBranchMatchesHead(t, env, bareDir)
	require.Empty(t, remoteCheckpointIDs(t, bareDir), "under manual, git push sends no checkpoints")
	require.Contains(t, output, "2 checkpoints waiting")
	require.Contains(t, output, "entire checkpoint push --all", "the suggested command must upload when run as printed")

	listing := listPolicyOutgoing(t, env)
	require.Equal(t, "manual", listing.Policy)
	require.Len(t, listing.Checkpoints, 2)
	outgoing, _ := listOutgoing(t, env)
	require.Equal(t, sortedIDs(auth, billing), outgoing.ids(), "waiting checkpoints stay queued")
}

func TestCheckpointPushPolicy_AutomaticIsTheDefault(t *testing.T) {
	t.Parallel()
	env := newOutgoingEnv(t)
	bareDir := env.SetupBareRemote()

	work := createCheckpointedCommit(t, env, "Some work", "work.go", "package work", "Some work")
	require.Equal(t, "automatic", listPolicyOutgoing(t, env).Policy)

	output := gitPushWithHooksIn(t, env, env.RepoDir)
	require.Equal(t, []string{work}, remoteCheckpointIDs(t, bareDir))
	require.NotContains(t, output, "waiting")
}

// The policy is per clone: the push queue is shared by every worktree, so a
// push from a linked worktree must see the policy set from the main one.
func TestCheckpointPushPolicy_AppliesToEveryWorktree(t *testing.T) {
	t.Parallel()
	env := newOutgoingEnv(t)
	bareDir := env.SetupBareRemote()
	work := createCheckpointedCommit(t, env, "Some work", "work.go", "package work", "Some work")

	linked := filepath.Join(t.TempDir(), "linked")
	testutil.RunGit(t, env.RepoDir, "worktree", "add", "-b", "linked-branch", linked)
	// The fixture leaves settings.json uncommitted; a real repository commits
	// it, so every worktree has Entire enabled.
	settingsJSON, err := os.ReadFile(filepath.Join(env.RepoDir, ".entire", "settings.json"))
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(linked, ".entire"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(linked, ".entire", "settings.json"), settingsJSON, 0o600))

	setCheckpointPushPolicy(t, env, "manual")
	gitPushWithHooksIn(t, env, linked)
	require.Empty(t, remoteCheckpointIDs(t, bareDir), "manual set from the main worktree must hold for a linked one")

	// Control: the same push uploads once the policy allows it, so the
	// assertion above is not passing because the linked worktree skips Entire.
	setCheckpointPushPolicy(t, env, "automatic")
	gitPushWithHooksIn(t, env, linked)
	require.Equal(t, []string{work}, remoteCheckpointIDs(t, bareDir))
}

// A policy file this binary cannot read must not read as automatic, and the
// warning names the file to fix.
func TestCheckpointPushPolicy_UnreadablePolicyUploadsNothing(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		plant     func(t *testing.T, env *TestEnv)
		namesFile bool
	}{
		{name: "empty", namesFile: true, plant: func(t *testing.T, env *TestEnv) {
			t.Helper()
			writeGitCommonDirFile(t, env, settings.CheckpointPushPolicyFile, "")
		}},
		{name: "unknown value", namesFile: true, plant: func(t *testing.T, env *TestEnv) {
			t.Helper()
			setCheckpointPushPolicy(t, env, "approved")
		}},
		{name: "symlink", plant: func(t *testing.T, env *TestEnv) {
			t.Helper()
			path := gitCommonDirFile(t, env, settings.CheckpointPushPolicyFile)
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
			planted := filepath.Join(filepath.Dir(path), "planted")
			require.NoError(t, os.WriteFile(planted, []byte("automatic\n"), 0o600))
			if err := os.Symlink("planted", path); err != nil {
				t.Skipf("cannot create symlink: %v", err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			env := newOutgoingEnv(t)
			bareDir := env.SetupBareRemote()
			createCheckpointedCommit(t, env, "Some work", "work.go", "package work", "Some work")
			tt.plant(t, env)

			output := gitPushWithHooksIn(t, env, env.RepoDir)

			remoteBranchMatchesHead(t, env, bareDir)
			require.Empty(t, remoteCheckpointIDs(t, bareDir), "an unreadable policy must not read as automatic")
			if tt.namesFile {
				require.Contains(t, output, "checkpoint-push-policy", "the warning names the file to fix")
			}
		})
	}
}

// A committed settings.json this binary cannot load (here, a key from a newer
// version) must not loosen manual. The push path's settings fall back to
// defaults on a load error (resolvePushSettings), so the policy is read from
// its own file alone.
func TestCheckpointPushPolicy_BrokenProjectSettingsKeepManual(t *testing.T) {
	t.Parallel()
	env := newOutgoingEnv(t)
	bareDir := env.SetupBareRemote()
	createCheckpointedCommit(t, env, "Some work", "work.go", "package work", "Some work")
	setCheckpointPushPolicy(t, env, "manual")
	env.PatchSettings(map[string]any{"setting_from_a_newer_version": true})

	gitPushWithHooksIn(t, env, env.RepoDir)

	remoteBranchMatchesHead(t, env, bareDir)
	require.Empty(t, remoteCheckpointIDs(t, bareDir))
}

// Released binaries rewrite clone preferences without the keys they do not
// model, for example when the trails cache refreshes. That must not turn a
// manual clone back into automatic.
func TestCheckpointPushPolicy_ManualSurvivesPreferencesRewrite(t *testing.T) {
	t.Parallel()
	env := newOutgoingEnv(t)
	bareDir := env.SetupBareRemote()
	env.RunCLI("configure", "--checkpoint-push-policy", "manual")
	writeGitCommonDirFile(t, env, settings.ClonePreferencesFile, `{"trails_enabled":true}`)
	createCheckpointedCommit(t, env, "Some work", "work.go", "package work", "Some work")

	gitPushWithHooksIn(t, env, env.RepoDir)

	remoteBranchMatchesHead(t, env, bareDir)
	require.Empty(t, remoteCheckpointIDs(t, bareDir), "an older binary's preferences rewrite must not release checkpoints")
}

func TestCheckpointPush_SendsOnlyTheNamedCheckpoint(t *testing.T) {
	t.Parallel()
	env := newOutgoingEnv(t)
	bareDir := env.SetupBareRemote()
	setCheckpointPushPolicy(t, env, "manual")
	auth := createCheckpointedCommit(t, env, "Add auth module", "auth.go", "package auth", "Add auth module")
	billing := createCheckpointedCommit(t, env, "Add billing module", "billing.go", "package billing", "Add billing module")
	gitPushWithHooksIn(t, env, env.RepoDir)

	result, err := runCheckpointPushJSON(t, env, false, auth+"@"+outgoingCommit(t, env, auth))
	require.NoError(t, err)
	require.Equal(t, "uploaded", result.status(t, auth))

	require.Equal(t, []string{auth}, remoteCheckpointIDs(t, bareDir))
	outgoing, _ := listOutgoing(t, env)
	require.Equal(t, []string{billing}, outgoing.ids(), "the checkpoint not named stays waiting")
}

func TestCheckpointPush_PinnedCommitThatMovedIsNotSent(t *testing.T) {
	t.Parallel()
	env := newOutgoingEnv(t)
	bareDir := env.SetupBareRemote()
	setCheckpointPushPolicy(t, env, "manual")

	session := env.NewSession()
	transcriptPath := session.CreateTranscript("Add cache layer", []FileChange{{Path: "cache.go", Content: "package cache"}})
	require.NoError(t, env.SimulateUserPromptSubmitWithPromptAndTranscriptPath(session.ID, "Add cache layer", transcriptPath))
	env.WriteFile("cache.go", "package cache")
	env.GitAdd("cache.go")
	env.GitCommitWithHooks("Add cache layer", "cache.go")
	cache := env.LatestCheckpointID()
	seen := outgoingCommit(t, env, cache)

	// Ending the turn rewrites the checkpoint after the user looked at it.
	session.TranscriptBuilder.AddAssistantMessage("Also tuned the eviction policy.")
	require.NoError(t, session.TranscriptBuilder.WriteToFile(transcriptPath))
	require.NoError(t, env.SimulateStop(session.ID, transcriptPath))
	require.NotEqual(t, seen, outgoingCommit(t, env, cache), "the stop hook must move the checkpoint for this test to mean anything")

	result, err := runCheckpointPushJSON(t, env, false, cache+"@"+seen)
	require.Error(t, err, "a checkpoint that did not upload is a failed push")
	require.Equal(t, "changed", result.status(t, cache))
	require.Empty(t, remoteCheckpointIDs(t, bareDir), "a version nobody looked at must not upload")
	outgoing, _ := listOutgoing(t, env)
	require.Equal(t, []string{cache}, outgoing.ids())
}

func TestCheckpointPush_RewriteAfterPushWaitsAgain(t *testing.T) {
	t.Parallel()
	env := newOutgoingEnv(t)
	bareDir := env.SetupBareRemote()
	setCheckpointPushPolicy(t, env, "manual")

	session := env.NewSession()
	transcriptPath := session.CreateTranscript("Add cache layer", []FileChange{{Path: "cache.go", Content: "package cache"}})
	require.NoError(t, env.SimulateUserPromptSubmitWithPromptAndTranscriptPath(session.ID, "Add cache layer", transcriptPath))
	env.WriteFile("cache.go", "package cache")
	env.GitAdd("cache.go")
	env.GitCommitWithHooks("Add cache layer", "cache.go")
	cache := env.LatestCheckpointID()

	_, err := runCheckpointPushJSON(t, env, false, cache+"@"+outgoingCommit(t, env, cache))
	require.NoError(t, err)
	pushed := env.RemoteCheckpointState(bareDir)

	session.TranscriptBuilder.AddAssistantMessage("Also tuned the eviction policy.")
	require.NoError(t, session.TranscriptBuilder.WriteToFile(transcriptPath))
	require.NoError(t, env.SimulateStop(session.ID, transcriptPath))

	outgoing, _ := listOutgoing(t, env)
	require.Equal(t, []string{cache}, outgoing.ids(), "the rewrite waits for the next checkpoint push")
	gitPushWithHooksIn(t, env, env.RepoDir)
	require.Equal(t, pushed, env.RemoteCheckpointState(bareDir), "git push still sends nothing under manual")

	_, err = runCheckpointPushJSON(t, env, false, cache+"@"+outgoingCommit(t, env, cache))
	require.NoError(t, err)
	require.NotEqual(t, pushed, env.RemoteCheckpointState(bareDir), "checkpoint push sends the rewrite")
	outgoing, _ = listOutgoing(t, env)
	require.Empty(t, outgoing.Checkpoints)
}

// Broad uploads need a person at a terminal; without one, a caller names the
// exact version it means to send.
func TestCheckpointPush_WithoutTerminalNeedsExactVersions(t *testing.T) {
	t.Parallel()
	env := newOutgoingEnv(t)
	bareDir := env.SetupBareRemote()
	setCheckpointPushPolicy(t, env, "manual")
	work := createCheckpointedCommit(t, env, "Some work", "work.go", "package work", "Some work")

	for _, args := range [][]string{{"--all"}, {work}} {
		_, stderr, err := runCheckpointPush(t, env, false, args...)
		require.Error(t, err, "checkpoint push %v without a terminal must be refused", args)
		require.Contains(t, stderr, "@", "the refusal shows the <id>@<commit> form: %s", stderr)
	}
	require.Empty(t, remoteCheckpointIDs(t, bareDir))

	_, err := runCheckpointPushJSON(t, env, false, work+"@"+outgoingCommit(t, env, work))
	require.NoError(t, err)
	require.Equal(t, []string{work}, remoteCheckpointIDs(t, bareDir))
}

func TestCheckpointPush_AllFromTerminal(t *testing.T) {
	t.Parallel()
	env := newOutgoingEnv(t)
	bareDir := env.SetupBareRemote()
	setCheckpointPushPolicy(t, env, "manual")
	auth := createCheckpointedCommit(t, env, "Add auth module", "auth.go", "package auth", "Add auth module")
	billing := createCheckpointedCommit(t, env, "Add billing module", "billing.go", "package billing", "Add billing module")

	result, err := runCheckpointPushJSON(t, env, true, "--all")
	require.NoError(t, err)
	require.Equal(t, "uploaded", result.status(t, auth))
	require.Equal(t, "uploaded", result.status(t, billing))
	require.Equal(t, sortedIDs(auth, billing), remoteCheckpointIDs(t, bareDir))
}

func TestCheckpointPush_UnknownCheckpointIsReported(t *testing.T) {
	t.Parallel()
	env := newOutgoingEnv(t)
	env.SetupBareRemote()
	setCheckpointPushPolicy(t, env, "manual")
	createCheckpointedCommit(t, env, "Some work", "work.go", "package work", "Some work")

	missing := "a1b2c3d4e5f6"
	result, err := runCheckpointPushJSON(t, env, false, missing+"@1111111111111111111111111111111111111111")
	require.Error(t, err)
	require.Equal(t, "not_waiting", result.status(t, missing))
}

// The zero hash is how the strategy spells "not pinned", so accepting it as a
// pin would let a caller without a terminal send a version it never saw.
func TestCheckpointPush_ZeroCommitIsNotAPin(t *testing.T) {
	t.Parallel()
	env := newOutgoingEnv(t)
	bareDir := env.SetupBareRemote()
	setCheckpointPushPolicy(t, env, "manual")
	work := createCheckpointedCommit(t, env, "Some work", "work.go", "package work", "Some work")

	_, _, err := runCheckpointPush(t, env, false, work+"@0000000000000000000000000000000000000000")
	require.Error(t, err)
	require.Empty(t, remoteCheckpointIDs(t, bareDir))
	outgoing, _ := listOutgoing(t, env)
	require.Equal(t, []string{work}, outgoing.ids())
}

func TestCheckpointPush_ConflictingPinsAreRefused(t *testing.T) {
	t.Parallel()
	env := newOutgoingEnv(t)
	bareDir := env.SetupBareRemote()
	setCheckpointPushPolicy(t, env, "manual")
	work := createCheckpointedCommit(t, env, "Some work", "work.go", "package work", "Some work")
	seen := outgoingCommit(t, env, work)
	other := strings.TrimSpace(testutil.RunGit(t, env.RepoDir, "rev-parse", "HEAD"))
	require.NotEqual(t, seen, other)

	_, _, err := runCheckpointPush(t, env, false, work+"@"+other, work+"@"+seen)
	require.Error(t, err, "two different versions of one checkpoint cannot both be the one to send")
	require.Empty(t, remoteCheckpointIDs(t, bareDir))

	result, err := runCheckpointPushJSON(t, env, false, work+"@"+seen, work+"@"+seen)
	require.NoError(t, err, "naming the same version twice is not a conflict")
	require.Equal(t, "uploaded", result.status(t, work))
}

func TestCheckpointPush_AllWithNothingWaitingStillReportsJSON(t *testing.T) {
	t.Parallel()
	env := newOutgoingEnv(t)
	env.SetupBareRemote()
	setCheckpointPushPolicy(t, env, "manual")

	result, err := runCheckpointPushJSON(t, env, true, "--all")
	require.NoError(t, err)
	require.NotNil(t, result.Results, "results must be an empty array, not missing")
	require.Empty(t, result.Results)
}

// A queued ref that cannot be read is still waiting; reporting it as
// not_waiting would tell the caller there is nothing left to send.
func TestCheckpointPush_UnreadableRefIsAFailure(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root reads files regardless of their mode")
	}
	env := newOutgoingEnv(t)
	bareDir := env.SetupBareRemote()
	setCheckpointPushPolicy(t, env, "manual")
	work := createCheckpointedCommit(t, env, "Some work", "work.go", "package work", "Some work")
	seen := outgoingCommit(t, env, work)

	var ref string
	for _, row := range listPolicyOutgoing(t, env).Checkpoints {
		if row.CheckpointID == work {
			ref = row.Ref
		}
	}
	commonDir := strings.TrimSpace(testutil.RunGit(t, env.RepoDir, "rev-parse", "--path-format=absolute", "--git-common-dir"))
	refFile := filepath.Join(commonDir, filepath.FromSlash(ref))
	require.FileExists(t, refFile, "the checkpoint ref must be loose for this test to mean anything")
	require.NoError(t, os.Chmod(refFile, 0o000))
	t.Cleanup(func() { _ = os.Chmod(refFile, 0o600) }) //nolint:errcheck // best-effort restore before TempDir cleanup

	result, err := runCheckpointPushJSON(t, env, false, work+"@"+seen)
	require.Error(t, err)
	require.Equal(t, "failed", result.status(t, work))
	require.Contains(t, result.Results[0].Error, "read checkpoint ref", "the failure must come from the ref read, not an earlier step")
	require.Empty(t, remoteCheckpointIDs(t, bareDir))
}

func TestCheckpointPush_RefusedWhenPushingIsDisabled(t *testing.T) {
	t.Parallel()
	env := newOutgoingEnv(t)
	bareDir := env.SetupBareRemote()
	env.PatchSettings(map[string]any{
		"strategy_options": map[string]any{"push_sessions": false},
	})
	work := createCheckpointedCommit(t, env, "Some work", "work.go", "package work", "Some work")

	_, stderr, err := runCheckpointPush(t, env, false, work+"@"+outgoingCommit(t, env, work))
	require.Error(t, err)
	require.Contains(t, stderr, "push_sessions")
	require.Empty(t, remoteCheckpointIDs(t, bareDir))
}

func TestCheckpointPushPolicy_ReadSurfaces(t *testing.T) {
	t.Parallel()
	env := newOutgoingEnv(t)
	env.SetupBareRemote()
	setCheckpointPushPolicy(t, env, "manual")
	work := createCheckpointedCommit(t, env, "Some work", "work.go", "package work", "Some work")

	listing := listPolicyOutgoing(t, env)
	require.Equal(t, "manual", listing.Policy)
	require.Len(t, listing.Checkpoints, 1)
	row := listing.Checkpoints[0]
	require.Equal(t, work, row.CheckpointID)
	require.Equal(t, strings.TrimSpace(testutil.RunGit(t, env.RepoDir, "rev-parse", row.Ref)), row.Commit)
	require.NotEmpty(t, row.SessionIDs)

	require.Contains(t, listOutgoingText(t, env), "entire checkpoint push --all")

	var status struct {
		Policy string `json:"checkpoint_push_policy"`
	}
	require.NoError(t, json.Unmarshal([]byte(cliStdout(t, env, "status", "--json")), &status))
	require.Equal(t, "manual", status.Policy)
	require.Contains(t, cliStdout(t, env, "status"), "entire checkpoint push --all")
}

// A bare `entire checkpoint push` only lists. Listing is not a failure, and
// it must say where to find the commit an <id>@<commit> argument needs.
func TestCheckpointPush_BareListsWaitingAndSucceeds(t *testing.T) {
	t.Parallel()
	env := newOutgoingEnv(t)
	bareDir := env.SetupBareRemote()
	setCheckpointPushPolicy(t, env, "manual")
	work := createCheckpointedCommit(t, env, "Some work", "work.go", "package work", "Some work")

	for _, tty := range []bool{true, false} {
		_, stderr, err := runCheckpointPush(t, env, tty)
		require.NoError(t, err, "listing is not a failure: %s", stderr)
		require.Contains(t, stderr, work)
		require.Contains(t, stderr, "entire checkpoint list --outgoing --json")
		if tty {
			require.Contains(t, stderr, "--all")
		} else {
			require.NotContains(t, stderr, "--all", "--all needs a terminal, so it is not offered without one")
		}
	}
	require.Empty(t, remoteCheckpointIDs(t, bareDir), "a bare push uploads nothing")
}

func TestCheckpointPushPolicy_ReadSurfacesReportUnreadablePolicy(t *testing.T) {
	t.Parallel()
	env := newOutgoingEnv(t)
	env.SetupBareRemote()
	setCheckpointPushPolicy(t, env, "approved")
	createCheckpointedCommit(t, env, "Some work", "work.go", "package work", "Some work")

	listing := listPolicyOutgoing(t, env)
	require.Empty(t, listing.Policy, "an unreadable policy is not reported as either value")
	require.Contains(t, listing.PolicyError, "approved")
}

func TestConfigure_SetsCheckpointPushPolicy(t *testing.T) {
	t.Parallel()
	env := newOutgoingEnv(t)
	env.SetupBareRemote()

	env.RunCLI("configure", "--checkpoint-push-policy", "manual")
	require.Equal(t, "manual", listPolicyOutgoing(t, env).Policy)

	env.RunCLI("configure", "--checkpoint-push-policy", "automatic")
	require.Equal(t, "automatic", listPolicyOutgoing(t, env).Policy)

	_, err := env.RunCLIWithError("configure", "--checkpoint-push-policy", "approved")
	require.Error(t, err)
	require.Equal(t, "automatic", listPolicyOutgoing(t, env).Policy, "a rejected value changes nothing")
}

// git-branch has no per-checkpoint refs for `entire checkpoint push` to send,
// so manual there would hold nothing back and leave no way to upload.
func TestConfigure_ManualPolicyNeedsGitRefs(t *testing.T) {
	t.Parallel()
	env := NewFeatureBranchEnv(t)
	env.SetupBareRemote()

	_, err := env.RunCLIWithError("configure", "--checkpoint-push-policy", "manual")
	require.Error(t, err)
	require.NoFileExists(t, gitCommonDirFile(t, env, settings.CheckpointPushPolicyFile), "a refused policy saves nothing")

	env.RunCLI("configure", "--checkpoint-push-policy", "automatic")

	env.RunCLI("configure", "--checkpoint-backend", "git-refs", "--checkpoint-push-policy", "manual")
	require.Equal(t, "manual", listPolicyOutgoing(t, env).Policy)
}

// The policy is saved only once the other flags have applied: a failed
// backend switch must not leave manual on a git-branch clone.
func TestConfigure_PolicyNotSavedWhenBackendSwitchFails(t *testing.T) {
	t.Parallel()
	if os.Geteuid() == 0 {
		t.Skip("root writes into read-only directories")
	}
	env := NewFeatureBranchEnv(t)
	env.SetupBareRemote()
	entireDir := filepath.Join(env.RepoDir, ".entire")
	require.NoError(t, os.Chmod(entireDir, 0o500))
	t.Cleanup(func() { _ = os.Chmod(entireDir, 0o750) }) //nolint:errcheck // best-effort restore before TempDir cleanup

	_, err := env.RunCLIWithError("configure", "--checkpoint-backend", "git-refs", "--checkpoint-push-policy", "manual")
	require.Error(t, err, "the backend save must fail for this test to mean anything")
	require.NoFileExists(t, gitCommonDirFile(t, env, settings.CheckpointPushPolicyFile), "manual must not be saved when the backend switch failed")
}
