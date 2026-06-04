package coreapi

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ogen-go/ogen/ogenerrors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/cmd/entire/cli/auth"
)

func TestAPIError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "nil error",
			err:  nil,
			want: "",
		},
		{
			name: "non-API error returns empty",
			err:  errors.New("dial tcp: connection refused"),
			want: "",
		},
		{
			name: "prefers detail",
			err: &ErrorModelStatusCode{
				StatusCode: 409,
				Response: ErrorModel{
					Title:  NewOptString("Conflict"),
					Detail: NewOptString("organization name already taken"),
				},
			},
			want: "organization name already taken",
		},
		{
			name: "falls back to title when detail empty",
			err: &ErrorModelStatusCode{
				StatusCode: 403,
				Response:   ErrorModel{Title: NewOptString("Forbidden")},
			},
			want: "Forbidden",
		},
		{
			name: "falls back to status when title and detail empty",
			err:  &ErrorModelStatusCode{StatusCode: 500},
			want: "control-plane request failed with status 500",
		},
		{
			name: "unwraps a wrapped API error",
			err: fmt.Errorf("create org: %w", &ErrorModelStatusCode{
				StatusCode: 422,
				Response:   ErrorModel{Detail: NewOptString("name is required")},
			}),
			want: "name is required",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := APIError(tc.err); got != tc.want {
				t.Errorf("APIError() = %q, want %q", got, tc.want)
			}
		})
	}
}

// makeJWT builds a three-segment JWT-shaped string with a non-"none" alg
// (so auth.CoreURLFromEnvToken's underlying ParseClaims accepts it) and the
// given payload. The signature segment is arbitrary — claims are parsed
// unverified. Mirrors cmd/entire/cli/auth.makeJWT; inlined because that
// helper is package-private.
func makeJWT(t *testing.T, payloadJSON string) string {
	t.Helper()
	enc := base64.RawURLEncoding
	header := enc.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))
	payload := enc.EncodeToString([]byte(payloadJSON))
	return header + "." + payload + "." + enc.EncodeToString([]byte("sig"))
}

// TestBearerAuth_EnvTokenBypass covers the CI / workload-identity path:
// when ENTIRE_TOKEN holds a JWT whose aud matches the bearerSource's
// resource origin, BearerAuth returns it verbatim without touching the
// keyring or contexts.json. This is the runner-friendly mode that lets
// `entire repo create` succeed on a host with no Secret Service /
// dbus-launch and no prior `entire login`.
//
// Not parallel — t.Setenv panics under t.Parallel and ENTIRE_TOKEN is
// process-global.
func TestBearerAuth_EnvTokenBypass(t *testing.T) {
	const coreOrigin = "https://core.us.entire.io"
	token := makeJWT(t, fmt.Sprintf(`{"sub":"ci-runner","aud":%q}`, coreOrigin))
	t.Setenv(auth.EnvTokenVar, token)

	src := &bearerSource{resourceBaseURL: coreOrigin}
	got, err := src.BearerAuth(context.Background(), "AnyOp")
	require.NoError(t, err)
	assert.Equal(t, token, got.Token, "BearerAuth must return ENTIRE_TOKEN verbatim when aud matches the control-plane origin")
}

// TestBearerAuth_EnvTokenAudMismatch verifies the trust gate. A JWT whose
// aud names a different core must be rejected with a clear error — never
// forwarded as a bearer to the wrong origin (the receiver would reject it,
// but the leak path is the request being sent at all).
func TestBearerAuth_EnvTokenAudMismatch(t *testing.T) {
	token := makeJWT(t, `{"sub":"ci-runner","aud":"https://core.eu.entire.io"}`)
	t.Setenv(auth.EnvTokenVar, token)

	src := &bearerSource{resourceBaseURL: "https://core.us.entire.io"}
	_, err := src.BearerAuth(context.Background(), "AnyOp")
	require.Error(t, err)
	assert.Contains(t, err.Error(), auth.EnvTokenVar)
	assert.Contains(t, err.Error(), "does not match")
}

// TestBearerAuth_EnvTokenBlank exercises the fail-closed contract: a set-
// but-empty (or whitespace-only) ENTIRE_TOKEN must error rather than fall
// through to the keyring, which would mask a misconfigured runner that
// thought it was setting the variable.
func TestBearerAuth_EnvTokenBlank(t *testing.T) {
	t.Setenv(auth.EnvTokenVar, "   \n")

	src := &bearerSource{resourceBaseURL: "https://core.us.entire.io"}
	_, err := src.BearerAuth(context.Background(), "AnyOp")
	require.Error(t, err)
	assert.Contains(t, err.Error(), auth.EnvTokenVar)
	assert.Contains(t, err.Error(), "blank")
}

// TestBearerAuth_EnvTokenAudNormalised confirms that the aud / resource
// comparison is canonicalised through api.NormalizeOriginURL on both sides,
// so a token whose aud differs only in case or default port matches the
// resource origin. Mirrors what api.AuthBaseURL does to the resource URL
// before bearerSource sees it.
func TestBearerAuth_EnvTokenAudNormalised(t *testing.T) {
	// aud carries an uppercase host and a trailing slash; the bearerSource
	// resource was already canonicalised (lowercase, no slash) by
	// api.OriginOnly at construction time. Both ends must normalise to the
	// same value for the gate to pass.
	token := makeJWT(t, `{"sub":"ci-runner","aud":"https://CORE.us.entire.io/"}`)
	t.Setenv(auth.EnvTokenVar, token)

	src := &bearerSource{resourceBaseURL: "https://core.us.entire.io"}
	got, err := src.BearerAuth(context.Background(), "AnyOp")
	require.NoError(t, err)
	assert.Equal(t, token, got.Token)
}

// bearerOnlySource mirrors the CLI's bearerSource contract: a fixed
// bearer token, and ErrSkipClientSecurity for sessionAuth so the
// generated middleware does NOT add a `Cookie: entire_session=` header.
// Used by TestBearerOnlySource_NoCookieOnTheWire to nail down the
// "bearer-only, no cookie" contract at the HTTP layer.
type bearerOnlySource struct{}

func (bearerOnlySource) BearerAuth(context.Context, OperationName) (BearerAuth, error) {
	return BearerAuth{Token: "test-bearer"}, nil
}

func (bearerOnlySource) SessionAuth(context.Context, OperationName) (SessionAuth, error) {
	return SessionAuth{}, ogenerrors.ErrSkipClientSecurity
}

// TestBearerOnlySource_NoCookieOnTheWire documents the SessionAuth
// empty-value contract by checking the wire: any operation issued by a
// Client built with a SessionAuth-skipping source must NOT carry a
// Cookie header. (ogen's securitySessionAuth unconditionally calls
// req.AddCookie, so returning SessionAuth{} with a nil error would send
// an empty `entire_session=` cookie; only ErrSkipClientSecurity prevents
// the cookie from being added.)
func TestBearerOnlySource_NoCookieOnTheWire(t *testing.T) {
	t.Parallel()

	// The handler runs on httptest's goroutine and the assertion runs
	// on the test goroutine; HTTP completion isn't a happens-before
	// edge the race detector recognises. Pass the captured header
	// across through a buffered channel so -race stays happy.
	cookieCh := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookieCh <- r.Header.Get("Cookie")
		w.Header().Set("Content-Type", "application/json")
		// Minimal valid ListOrgMembersOutputBody payload so the response
		// decoder doesn't blow up; we only care about the inbound headers.
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"members":[]}`)); err != nil {
			t.Errorf("writing test response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(srv.URL, bearerOnlySource{})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	// ListOrgMembers is a simple GET that exercises the security
	// middleware; the result itself is irrelevant to this test.
	if _, err := c.ListOrgMembers(context.Background(), ListOrgMembersParams{OrgId: "01H000000000000000000000A1"}); err != nil {
		t.Fatalf("ListOrgMembers: %v", err)
	}

	cookieHeader := <-cookieCh
	if cookieHeader != "" {
		t.Errorf("outbound Cookie header = %q, want empty (bearer-only contract)", cookieHeader)
	}
}
