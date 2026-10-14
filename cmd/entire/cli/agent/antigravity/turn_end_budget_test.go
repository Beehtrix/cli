package antigravity

import (
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/stretchr/testify/require"
)

// The finalize budget must stay inside the host's turn-end hook timeout, so
// Entire stops itself before the host kills the hook.
func TestTurnEndBudgetFitsTheHookTimeout(t *testing.T) {
	t.Parallel()

	budgeter, ok := agent.AsTurnEndBudgeter(NewAntigravityAgent())
	require.True(t, ok)
	require.Equal(t, agent.TurnEndBudgetWithin(time.Duration(stopHookTimeoutSeconds)*time.Second), budgeter.TurnEndBudget())
	require.Positive(t, budgeter.TurnEndBudget())
	require.Less(t, budgeter.TurnEndBudget(), time.Duration(stopHookTimeoutSeconds)*time.Second)
}
