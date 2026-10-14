package cli

import (
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/agent/types"
	"github.com/entireio/cli/cmd/entire/cli/agent/vogon"
	"github.com/stretchr/testify/require"
)

func TestTurnEndFinalizeDeadline(t *testing.T) {
	t.Parallel()

	claude, err := agent.GetByAgentType(agent.AgentTypeClaudeCode)
	require.NoError(t, err)
	budgeter, ok := agent.AsTurnEndBudgeter(claude)
	require.True(t, ok)
	require.Equal(t, processStart.Add(budgeter.TurnEndBudget()), turnEndFinalizeDeadline(agent.AgentTypeClaudeCode))

	fallback := processStart.Add(agent.TurnEndBudgetWithin(defaultTurnEndHookTimeout))
	require.Equal(t, fallback, turnEndFinalizeDeadline(agent.AgentTypeOpenCode), "an agent without a declared budget gets the default")
	require.Equal(t, fallback, turnEndFinalizeDeadline(types.AgentType("future-agent")), "an unknown agent gets the default")
}

// turnEndBudgetFallbacks lists the agents that deliberately take
// defaultTurnEndHookTimeout instead of declaring agent.TurnEndBudgeter, with
// the reason. Every other agent must declare its budget, so a new agent cannot
// inherit the default without someone deciding it should.
var turnEndBudgetFallbacks = map[types.AgentName]string{
	agent.AgentNameCursor:   "Cursor documents only a \"platform default\" hook timeout, with no number",
	vogon.AgentNameVogon:    "deterministic test agent with no host process to time it out",
	agent.AgentNameOpenCode: "OpenCode applies no hook timeout; Entire's plugin blocks on the hook, so the default is the only bound",
}

func TestTurnEndBudgetLedger(t *testing.T) {
	t.Parallel()

	for _, name := range agent.List() {
		ag, err := agent.Get(name)
		require.NoError(t, err)
		// External agents always take the default (no protocol field carries a
		// hook timeout), and other tests in this package register them globally.
		if _, external := ag.(agent.CapabilityDeclarer); external {
			continue
		}
		_, declares := agent.AsTurnEndBudgeter(ag)
		reason, listed := turnEndBudgetFallbacks[name]
		switch {
		case declares && listed:
			t.Errorf("%s declares agent.TurnEndBudgeter; remove its stale turnEndBudgetFallbacks entry", name)
		case !declares && !listed:
			t.Errorf("%s neither declares agent.TurnEndBudgeter nor appears in turnEndBudgetFallbacks; derive its budget from its turn-end hook timeout, or list it with the reason it takes the default", name)
		case listed:
			require.NotEmpty(t, reason, "%s needs a reason", name)
		}
	}
	for name := range turnEndBudgetFallbacks {
		_, err := agent.Get(name)
		require.NoError(t, err, "turnEndBudgetFallbacks lists %s, which is not a registered agent", name)
	}
}
