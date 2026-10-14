package claudecode

import (
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

var _ agent.TurnEndBudgeter = (*ClaudeCodeAgent)(nil)

// TurnEndBudget implements agent.TurnEndBudgeter. Entire installs the turn-end hooks with checkpointHookTimeoutSec.
func (*ClaudeCodeAgent) TurnEndBudget() time.Duration {
	return agent.TurnEndBudgetWithin(time.Duration(checkpointHookTimeoutSec) * time.Second)
}
