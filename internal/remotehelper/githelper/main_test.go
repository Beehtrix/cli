package githelper

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
)

// Environment handed to the test binary when a git subprocess runs it
// as a remote helper (see helperStub in force_with_lease_test.go).
const (
	helperServerEnv   = "GITHELPER_TEST_HELPER_SERVER"
	helperProtocolEnv = "GITHELPER_TEST_HELPER_PROTOCOL"
)

// TestMain lets the test binary double as a git remote helper: when
// git spawns it with helperServerEnv set, it runs the real protocol
// loop against that server instead of the test suite. Exiting here,
// not from a test, keeps the testing package's "PASS" line off the
// helper's stdout, which git reads as protocol.
func TestMain(m *testing.M) {
	serverURL := os.Getenv(helperServerEnv)
	if serverURL == "" {
		os.Exit(m.Run())
	}
	protocolVersion := 2
	if v := os.Getenv(helperProtocolEnv); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bad %s=%q: %v\n", helperProtocolEnv, v, err)
			os.Exit(1)
		}
		protocolVersion = n
	}
	if err := Run(context.Background(), testTransportURL(serverURL), protocolVersion, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "test helper: %v\n", err)
		os.Exit(1)
	}
	os.Exit(0)
}
