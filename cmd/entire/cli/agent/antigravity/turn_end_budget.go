package antigravity

import (
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

var _ agent.TurnEndBudgeter = (*AntigravityAgent)(nil)

// TurnEndBudget implements agent.TurnEndBudgeter. Entire installs the Stop hook
// with stopHookTimeoutSeconds, and RepairHookTimeouts adds it to older installs.
func (*AntigravityAgent) TurnEndBudget() time.Duration {
	return agent.TurnEndBudgetWithin(time.Duration(stopHookTimeoutSeconds) * time.Second)
}
