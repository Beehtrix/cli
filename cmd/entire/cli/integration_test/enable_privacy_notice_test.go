//go:build integration

package integration

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Agents and CI run `entire enable` without a terminal, so the notice that
// checkpoints carry transcripts to the push destination must reach that output
// too, on every path, and be the last thing printed.
func TestEnable_PrivacyNotice(t *testing.T) {
	t.Parallel()
	const (
		privacy = "  They contain your agent transcripts. If that repo is public, anyone can read them.\n" +
			"  Secret redaction is best effort, not bulletproof. Review with `entire checkpoint explain`.\n" +
			"  To stop uploads: `entire configure --skip-push-sessions`\n" +
			"  To use a separate private repo: `entire configure --checkpoint-remote github:<owner>/<repo>`\n"
		redaction = "  Secret redaction is best effort, not bulletproof. Review with `entire checkpoint explain`.\n"
	)
	enableAgent := []string{"enable", "--agent", agentClaudeCode, "--telemetry=false"}
	for _, tc := range []struct {
		name     string
		noRemote bool
		before   []string
		args     []string
		// after is setup output the notice must follow.
		after, notice string
	}{
		{name: "agent", args: enableAgent, after: "Ready.", notice: "Checkpoints will be uploaded when you push to origin.\n" + privacy},
		{name: "yes", args: []string{"enable", "--yes", "--telemetry=false"}, after: "Ready.", notice: "Checkpoints will be uploaded when you push to origin.\n" + privacy},
		{name: "rerun", before: enableAgent, args: []string{"enable"}, after: "Entire is already enabled.", notice: "Checkpoints will be uploaded when you push to origin.\n" + privacy},
		{name: "push disabled", args: append(enableAgent, "--skip-push-sessions"), after: "Ready.", notice: "Checkpoint uploads are off (push_sessions: false). Checkpoints stay in this clone.\n" + redaction},
		{name: "no remote", noRemote: true, args: enableAgent, after: "Ready.", notice: "No Git remotes configured. Checkpoints stay local until a remote is configured.\n" + privacy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			env := freshRepoEnv(t)
			if !tc.noRemote {
				env.SetupBareRemote()
			}
			if tc.before != nil {
				env.RunCLI(tc.before...)
			}
			out := env.RunCLI(tc.args...)
			require.Contains(t, out, tc.after)
			require.True(t, strings.HasSuffix(out, "\n"+tc.notice), "notice should end the output; got:\n%s", out)
			require.Less(t, strings.Index(out, tc.after), strings.LastIndex(out, tc.notice), "notice should follow %q; got:\n%s", tc.after, out)
		})
	}
}
