package httpclient_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/cmd/entire/cli/testutil"
)

const clientBypassPattern = `&[[:space:]]*http\.Client[[:space:]]*\{|http\.Client\{|new[[:space:]]*\([[:space:]]*http\.Client|` +
	`(^|[;{])[[:space:]]*(var[[:space:]]+)?[[:alnum:]_,[:space:]]+[[:space:]]+http\.Client([[:space:];=]|$)|` +
	`http\.(DefaultClient|Get|Head|Post|PostForm)([^[:alnum:]_]|$)`

// TestHTTPClientsUseSecureConstructor catches client allocations, zero-value
// declarations and package-level helpers that bypass the shared constructor.
func TestHTTPClientsUseSecureConstructor(t *testing.T) {
	t.Parallel()
	root, ok := testutil.GitGrepGuardRepoRoot(t)
	if !ok {
		return
	}
	out := testutil.GitGrepGuard(t, root, "-l", "--extended-regexp", "--",
		clientBypassPattern, "--", ":(glob)**/*.go", ":(exclude,glob)**/*_test.go")
	// Explicit exceptions, not a blanket exemption for generated code:
	// - generated coreapi config has a default, overridden by CLI constructors;
	// - discovery copies its fallback client and forbids every redirect;
	// - the constructor owns the sole allocation;
	// - the wrapper copies DefaultClient and installs the secure policy.
	require.Equal(t, []string{
		"internal/coreapi/oas_cfg_gen.go",
		"internal/entireclient/clusterdiscovery/discovery.go",
		"internal/entireclient/httpclient/client.go",
		"internal/entireclient/httpclient/redirect.go",
	}, strings.Fields(out), "use httpclient.New; audit any new bypass (zero matches also fails)")
}

func TestClientBypassPattern(t *testing.T) {
	t.Parallel()
	pattern := regexp.MustCompile(clientBypassPattern)
	require.False(t, pattern.MatchString("// checkRedirect is the http.Client CheckRedirect hook."))
	for _, source := range []string{
		"c := &http.Client{}", "c := http.Client{}", "c := new(http.Client)",
		"var c http.Client", "var c, d http.Client", " c http.Client", // grouped var or embedded value field
		"http.Get(url)", "http.Head(url)", "http.Post(url, kind, body)", "http.PostForm(url, values)",
		"c := http.DefaultClient", "http.DefaultClient.Do(req)",
	} {
		t.Run(source, func(t *testing.T) {
			t.Parallel()
			require.True(t, pattern.MatchString(source))
		})
	}
}
