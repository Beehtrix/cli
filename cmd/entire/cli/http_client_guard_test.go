package cli

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

// TestHTTPClientsUseSecureConstructor prevents production client literals from
// silently omitting the redirect policy. Tests may supply deliberately unsafe
// clients to demonstrate the boundary; production uses httpclient.New.
func TestHTTPClientsUseSecureConstructor(t *testing.T) {
	t.Parallel()
	root, ok := testutil.GitGrepGuardRepoRoot(t)
	if !ok {
		return
	}
	out := testutil.GitGrepGuard(t, root, "-l", "--extended-regexp", "--",
		`&[[:space:]]*http\.Client[[:space:]]*\{|http\.Client\{|new[[:space:]]*\([[:space:]]*http\.Client`, "--",
		":(glob)**/*.go", ":(exclude,glob)**/*_test.go")
	require.Equal(t, []string{"internal/entireclient/httpclient/client.go"}, strings.Fields(out),
		"HTTP clients must use httpclient.New; only its implementation may allocate a bare client (zero matches also fails)")
}
