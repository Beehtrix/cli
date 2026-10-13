package httpclient

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
)

const (
	// SchemeHTTPS is the secure scheme required for production credential transport.
	SchemeHTTPS = "https"
	// SchemeHTTP is permitted only by callers' explicit insecure-origin policies.
	SchemeHTTP   = "http"
	maxRedirects = 10
)

var (
	// ErrRequestPolicy identifies a request refused for security policy reasons.
	ErrRequestPolicy = errors.New("request rejected by policy")
	// ErrRedirectPolicy identifies a redirect rejected by the shared security policy.
	ErrRedirectPolicy = fmt.Errorf("%w: redirect refused", ErrRequestPolicy)
	// ErrInsecureRedirect identifies a redirect that would leave HTTPS.
	ErrInsecureRedirect = fmt.Errorf("%w: refusing redirect from HTTPS to an insecure scheme", ErrRedirectPolicy)
	// ErrTooManyRedirects identifies a redirect chain exceeding the hop limit.
	// A loop is an availability failure, not a security rejection, so it does
	// not match ErrRequestPolicy: callers treat it like any other node failure.
	ErrTooManyRedirects = errors.New("stopped after " + strconv.Itoa(maxRedirects) + " redirects")
)

// CheckSecureRedirect is a CheckRedirect policy that limits redirect chains and
// refuses to leave HTTPS once reached. Host restrictions belong to the caller.
func CheckSecureRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= maxRedirects {
		return ErrTooManyRedirects
	}
	if req.URL.Scheme != SchemeHTTPS {
		for _, previous := range via {
			if previous.URL.Scheme == SchemeHTTPS {
				return ErrInsecureRedirect
			}
		}
	}
	return nil
}

// WithSecureRedirects returns a copy of client with the shared redirect policy
// applied to redirects its callback permits. Callback errors take precedence:
// ErrUseLastResponse safely exposes a 3xx response without following it. A nil
// client uses http.DefaultClient. Transport, timeout and jar are preserved;
// neither the supplied client nor the default client is mutated.
func WithSecureRedirects(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	guarded := *client
	policy := client.CheckRedirect
	guarded.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		// Remember the proposed URL before the callback can rewrite it, but
		// do not reject a redirect the callback declines to follow.
		proposedErr := CheckSecureRedirect(req, via)
		if policy != nil {
			proposedScheme := req.URL.Scheme
			if err := policy(req, via); err != nil {
				// net/http compares ErrUseLastResponse by identity; do not wrap.
				return err
			}
			if proposedErr != nil {
				return proposedErr
			}
			// The first HTTPS upgrade is not yet in via. Do not let a
			// callback undo it, even when every previous hop was HTTP.
			if proposedScheme == SchemeHTTPS && req.URL.Scheme != SchemeHTTPS {
				return ErrInsecureRedirect
			}
			return CheckSecureRedirect(req, via)
		}
		return proposedErr
	}
	return &guarded
}
