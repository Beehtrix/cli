package copilotcli

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

	budgeter, ok := agent.AsTurnEndBudgeter(NewCopilotCLIAgent())
	require.True(t, ok)
	require.Equal(t, agent.TurnEndBudgetWithin(time.Duration(hostDefaultHookTimeoutSec)*time.Second), budgeter.TurnEndBudget())
	require.Positive(t, budgeter.TurnEndBudget())
	require.Less(t, budgeter.TurnEndBudget(), time.Duration(hostDefaultHookTimeoutSec)*time.Second)
}
