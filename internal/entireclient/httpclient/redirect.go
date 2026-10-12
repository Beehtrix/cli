package httpclient

import (
	"errors"
	"fmt"
	"net/http"
)

const (
	// SchemeHTTPS is the secure scheme required for production credential transport.
	SchemeHTTPS = "https"
	// SchemeHTTP is permitted only by callers' explicit insecure-origin policies.
	SchemeHTTP   = "http"
	maxRedirects = 10
)

var (
	// ErrRedirectPolicy identifies a redirect rejected by the shared security policy.
	ErrRedirectPolicy = errors.New("redirect refused")
	// ErrInsecureRedirect identifies a redirect that would leave HTTPS.
	ErrInsecureRedirect = fmt.Errorf("%w: refusing redirect from HTTPS to an insecure scheme", ErrRedirectPolicy)
	// ErrTooManyRedirects identifies a redirect chain exceeding the hop limit.
	ErrTooManyRedirects = fmt.Errorf("%w: stopped after %d redirects", ErrRedirectPolicy, maxRedirects)
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
// applied before and after its existing callback. A nil client uses
// http.DefaultClient. Transport, timeout and jar are preserved; neither the
// supplied client nor the default client is mutated.
func WithSecureRedirects(client *http.Client) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	guarded := *client
	policy := client.CheckRedirect
	guarded.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := CheckSecureRedirect(req, via); err != nil {
			return err
		}
		if policy != nil {
			proposedScheme := req.URL.Scheme
			if err := policy(req, via); err != nil {
				// net/http compares ErrUseLastResponse by identity; do not wrap.
				return err
			}
			// The first HTTPS upgrade is not yet in via. Do not let a
			// callback undo it, even when every previous hop was HTTP.
			if proposedScheme == SchemeHTTPS && req.URL.Scheme != SchemeHTTPS {
				return ErrInsecureRedirect
			}
			return CheckSecureRedirect(req, via)
		}
		return nil
	}
	return &guarded
}
