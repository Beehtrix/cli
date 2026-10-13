package httpclient

import (
	"net/http"
	"time"
)

// Options configures an HTTP client without allowing callers to omit the shared
// redirect policy. CheckRedirect may impose stricter, client-specific rules.
type Options struct {
	Transport     http.RoundTripper
	Timeout       time.Duration
	CheckRedirect func(*http.Request, []*http.Request) error
}

// New returns an HTTP client that rejects HTTPS downgrades by default. It
// preserves Go's sensitive-header handling and composes any supplied callback.
func New(options Options) *http.Client {
	return WithSecureRedirects(&http.Client{
		Transport:     options.Transport,
		Timeout:       options.Timeout,
		CheckRedirect: options.CheckRedirect,
	})
}
