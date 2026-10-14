package antigravity

import (
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

var _ agent.TurnEndBudgeter = (*AntigravityAgent)(nil)

// TurnEndBudget implements agent.TurnEndBudgeter. Entire installs the Stop hook with stopHookTimeoutSeconds.
func (*AntigravityAgent) TurnEndBudget() time.Duration {
	return agent.TurnEndBudgetWithin(time.Duration(stopHookTimeoutSeconds) * time.Second)
}
