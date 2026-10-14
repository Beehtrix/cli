package pi

import (
	"fmt"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/stretchr/testify/require"
)

// The finalize budget must stay inside the host's turn-end hook timeout, so
// Entire stops itself before the host kills the hook.
func TestTurnEndBudgetFitsTheHookTimeout(t *testing.T) {
	t.Parallel()

	budgeter, ok := agent.AsTurnEndBudgeter(NewPiAgent())
	require.True(t, ok)
	require.Equal(t, agent.TurnEndBudgetWithin(extensionHookTimeout), budgeter.TurnEndBudget())
	require.Positive(t, budgeter.TurnEndBudget())
	require.Less(t, budgeter.TurnEndBudget(), extensionHookTimeout)
}

// extensionHookTimeout must match the execFile timeout the embedded extension
// actually applies, or the budget is sized against a limit pi never enforces.
func TestExtensionHookTimeoutMatchesTheExtension(t *testing.T) {
	t.Parallel()

	require.Contains(t, extensionTemplate, fmt.Sprintf("timeout: %d,", extensionHookTimeout.Milliseconds()))
}
