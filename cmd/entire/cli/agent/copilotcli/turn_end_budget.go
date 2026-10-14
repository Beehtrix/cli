package copilotcli

import (
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// hostDefaultHookTimeoutSec is Copilot CLI's default hook timeout, which applies
// to agentStop because Entire installs it without a timeoutSec. Copilot kills a
// hook that outlasts it and continues: "timeoutSec ... Default: 30"
// (https://docs.github.com/en/copilot/reference/hooks-configuration).
const hostDefaultHookTimeoutSec = 30

var _ agent.TurnEndBudgeter = (*CopilotCLIAgent)(nil)

// TurnEndBudget implements agent.TurnEndBudgeter. Sized against the host default, since Entire sets no timeoutSec.
func (*CopilotCLIAgent) TurnEndBudget() time.Duration {
	return agent.TurnEndBudgetWithin(time.Duration(hostDefaultHookTimeoutSec) * time.Second)
}
