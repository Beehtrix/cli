package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/cmd/entire/cli/api"
	"github.com/entireio/cli/cmd/entire/cli/review"
)

// clearAgentCallerEnv makes the test process look like a human's shell to
// review.DetectAgentCaller, whatever agent runs the test suite.
func clearAgentCallerEnv(t *testing.T) {
	t.Helper()
	for _, name := range review.AgentCallerEnvVars() {
		t.Setenv(name, "")
	}
}

// Every comment write in both trail models refuses an agent caller before any
// trail resolution or network call and names the findings alternative, but
// never the override, which stays undiscoverable. list and show stay open and
// carry no override.
//
// Not parallel: sets the agent-caller environment.
func TestTrailCommentWritesRefuseAgentCallers(t *testing.T) {
	clearAgentCallerEnv(t)
	t.Setenv("CLAUDECODE", "1")

	writes := map[string][]string{
		"add":       {"-m", "hi"},
		"reply":     {"discussion-one", "-m", "hi"},
		"edit":      {"discussion-one", "message-one", "-m", "hi"},
		"delete":    {"discussion-one", "message-one", "--force"},
		"resolve":   {"discussion-one"},
		"unresolve": {"discussion-one"},
	}
	for _, project := range []bool{true, false} {
		comment, _, err := newTrailCmdForMode(project).Find([]string{"comment"})
		require.NoError(t, err)
		for _, child := range comment.Commands() {
			name := child.Name()
			flag := child.Flags().Lookup(trailCommentHumanOverrideFlag)
			if name == cmdList || name == discussionShow {
				assert.Nil(t, flag, "read verb %q must not take the override", name)
				continue
			}
			require.NotNil(t, flag, "write verb %q (project=%v) is not guarded", name, project)
			assert.True(t, flag.Hidden, "the override must stay out of help")
			args, ok := writes[name]
			require.True(t, ok, "unexpected write verb %q: add its arguments here", name)

			cmd := newTrailCmdForMode(project)
			cmd.SilenceUsage, cmd.SilenceErrors = true, true
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(append([]string{"comment", name, "--trail", "1"}, args...))
			err := cmd.Execute()
			require.Error(t, err, "%s (project=%v)", name, project)
			for _, want := range []string{"a Claude Code session", "entire trail finding"} {
				assert.Contains(t, err.Error(), want, "%s (project=%v)", name, project)
			}
			assert.NotContains(t, err.Error(), trailCommentHumanOverrideFlag, "the refusal must not name the override")
		}
	}
}

// With the override, an agent caller's write goes through.
//
// Not parallel: sets the agent-caller environment and replaces the project
// routing constructors.
func TestTrailCommentHumanOverrideLetsAgentWrite(t *testing.T) {
	clearAgentCallerEnv(t)
	t.Setenv("CLAUDECODE", "1")
	calls := 0
	setupProjectTrailTest(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		assert.Equal(t, http.MethodPost, r.Method)
		assert.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"discussion": api.TrailDiscussionSummary{ID: "discussion-one", Title: "Plan", TrailID: projectTrailTestID},
		}))
	})
	_, _, err := executeProjectTrailTest(t, "comment", "add", "-m", "Plan", "--human-says-do-it",
		"--project", "gh/acme", "--trail", projectTrailTestID)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}
