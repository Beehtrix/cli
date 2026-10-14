package factoryaidroid

import (
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
)

// hostDefaultHookTimeoutSec is Factory AI Droid's default hook timeout, which
// applies to Stop because Entire installs it without a timeout: "Per-command
// timeout in seconds. Defaults to 60"
// (https://docs.factory.com/reference/hooks-reference).
const hostDefaultHookTimeoutSec = 60

var _ agent.TurnEndBudgeter = (*FactoryAIDroidAgent)(nil)

// TurnEndBudget implements agent.TurnEndBudgeter. Sized against the host default, since Entire sets no timeout.
func (*FactoryAIDroidAgent) TurnEndBudget() time.Duration {
	return agent.TurnEndBudgetWithin(time.Duration(hostDefaultHookTimeoutSec) * time.Second)
}
