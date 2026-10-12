package dispatch

import (
	"net/http"
	"testing"

	"github.com/entireio/cli/cmd/entire/cli/api"
	"github.com/entireio/cli/internal/entireclient/httpclient/httpclienttest"
)

func TestCloudClient_RejectsTLSDowngrade(t *testing.T) {
	// Not parallel: the shared API constructor captures http.DefaultTransport.
	// Swap only the network base, preserving its bearer and redirect policy.
	httpclienttest.CheckRejectsDowngradeSerial(t, func(t *testing.T, origin string, rt http.RoundTripper) error {
		t.Helper()
		original := http.DefaultTransport
		http.DefaultTransport = rt
		t.Cleanup(func() { http.DefaultTransport = original })
		c := NewCloudClient(api.NewClientWithBaseURL("test-token", origin))
		_, err := c.CreateDispatch(t.Context(), CreateDispatchRequest{}, "")
		return err
	})
}
