package codex

import (
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

var _ agent.TurnEndBudgeter = (*CodexAgent)(nil)

// TurnEndBudget implements agent.TurnEndBudgeter. Entire installs the Stop hook with defaultHookTimeoutSec.
func (*CodexAgent) TurnEndBudget() time.Duration {
	return agent.TurnEndBudgetWithin(time.Duration(defaultHookTimeoutSec) * time.Second)
}
