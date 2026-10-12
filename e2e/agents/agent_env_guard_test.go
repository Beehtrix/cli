package agents

import (
	"slices"
	"strings"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

// TestAgentEnvIsTheOnlyEnvFilter fails when a runner builds an agent's
// environment without agentEnv: by filtering os.Environ() itself, or by handing
// it over whole. Either way the agent can inherit harnessOnlyEnv, and the hooks
// it runs then skip the prompt detection E2E exists to exercise.
func TestAgentEnvIsTheOnlyEnvFilter(t *testing.T) {
	t.Parallel()
	repoRoot, ok := testutil.GitGrepGuardRepoRoot(t)
	if !ok {
		return
	}
	out := testutil.GitGrepGuard(t, repoRoot, "-n", "-E",
		`filterEnv\(|append\(os\.Environ\(\)|Env = os\.Environ\(\)`,
		"--", "e2e/agents/*.go", ":(exclude)e2e/agents/*_test.go")

	// agentEnv's own call and filterEnv's definition.
	allowed := []string{
		"return filterEnv(env, append(slices.Clone(harnessOnlyEnv), extra...)...)",
		"func filterEnv(env []string, names ...string) []string {",
	}
	var seen int
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		file, rest, _ := strings.Cut(line, ":")
		_, code, _ := strings.Cut(rest, ":")
		if file == "e2e/agents/agent.go" && slices.Contains(allowed, strings.TrimSpace(code)) {
			seen++
			continue
		}
		t.Errorf("%s builds an agent env without agentEnv; use agentEnv(os.Environ(), ...) so harnessOnlyEnv is dropped", line)
	}
	if seen != len(allowed) {
		t.Fatalf("matched %d of agentEnv's own %d lines; the guard pattern has gone stale:\n%s", seen, len(allowed), out)
	}
}
