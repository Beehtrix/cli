package redact

import (
	"regexp"
	"strings"
)

// Provider-specific deterministic secret patterns.
//
// Detection here is purely prefix + length based: it never depends on
// entropy or the surrounding key name, so it catches low-entropy
// credential formats the other secret layers don't reliably flag.
//
// The betterleaks layer's coverage of these differs per prefix (verified
// against the upstream betterleaks v1.8.0 rule source):
//   - sb_secret_: the supabase-project-api-key rule is a *composite* rule
//     (a required supabase-project-url component, the 1.8.0 spelling of
//     RequiredRules) that only fires when a matching "*.supabase.co" URL is
//     present in the same content, on top of an exact 31-char body and an
//     entropy<=4.0 filter. A secret captured on its own therefore passes
//     straight through regardless of entropy.
//   - sbp_: the supabase-management-token rule fires standalone (no
//     required components), but only matches an exact 40-character
//     lowercase body and is further filtered by entropy<=3.5 and a
//     two-digit minimum. A high-entropy 40-char sbp_ token captured alone
//     IS caught by betterleaks; what this layer adds for sbp_ is coverage
//     of bodies at other lengths, lower entropy, or without two digits.
//
// Supabase (https://supabase.com/docs/guides/getting-started/api-keys):
//   - sb_secret_...      secret API key (replaces the legacy service_role
//     key; bypasses row-level security, server-side
//     only) — SENSITIVE, always redacted.
//   - sbp_...            personal access token used by the Supabase CLI and
//     Management API — SENSITIVE, always redacted.
//   - sb_publishable_... publishable key (replaces the legacy anon key). It
//     is designed to be embedded in client-side code and
//     is protected by row-level security, so it is NOT a
//     secret and is intentionally NOT redacted here.
//     Redacting it would be false-positive noise; this
//     matches betterleaks, which also ships no
//     publishable-key rule.
//
// The current real key bodies are 31 chars (sb_secret_) and 40 chars
// (sbp_); charsets mirror betterleaks (sb_secret_ keys are mixed-case
// base64url, sbp_ tokens are lowercase). The {20,} floor comfortably
// catches the current and plausibly-longer future formats while rejecting
// short identifier-like collisions such as "sb_secret_short".
//
// Known false-positive class: because the body charset includes `_` and
// the {20,} length check is open-ended, sufficiently long snake_case
// identifiers that merely start with a provider prefix are redacted even
// though they aren't secrets — e.g. `sb_secret_key_rotation_handler`, or
// mid-word inside a longer identifier like
// `libsbp_something_long_enough_value`. This is
// accepted: over-redaction is the safe direction here (see
// TestString_SupabaseProviderTokenLongIdentifierOverRedaction), and adding
// anchors or capping the body length to eliminate it would reopen the
// low-entropy under-redaction gap below.
//
// The prefix is deliberately NOT preceded by a \b word boundary. \b requires
// the character before the prefix to be a non-word char, so a secret glued to
// a preceding word character — an underscore-joined name (FOO_sb_secret_…) or,
// in the JSONL fall-back / raw redact.Bytes path, a JSON escape whose trailing
// letter abuts the prefix (…line1\nsb_secret_…, where the byte before "sb" is
// the literal 'n') — would slip past. Because these low-entropy secrets are
// backed up by no other layer, missing them means the raw key reaches the
// checkpoint blob. Dropping the anchor is redaction-completeness-safe: any
// high-entropy incidental match would already be caught by the entropy layer,
// and a mid-word identifier collision (documented above) only ever
// over-redacts, never under-redacts.
var providerTokenPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sb_secret_[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`sbp_[a-z0-9_-]{20,}`),
}

// Bare sk- keys (issue #2728): OpenAI-compatible gateways mint keys that are
// "sk-" plus a short base64url body — LiteLLM proxy virtual keys are sk- + 22
// chars. The entropy layer cannot catch these: a match of n chars has entropy
// at most log2(n), so a 25-char key must have at most one repeated character
// to clear the 4.5 threshold, and ~96% of random keys of this shape do not.
// betterleaks only ships rules for specific sub-prefixes (sk-ant-, sk-or-v1-,
// sk-lf-, sk-api-), not the bare prefix with a short body. A NAME=value
// assignment happened to be caught only because secretPattern's class
// includes '_' and '=', so the name lengthened the match and its entropy.
//
// Unlike the Supabase prefixes, "sk-" is common inside ordinary words
// ("risk-", "task-", "disk-"), so this rule anchors the prefix: it must start
// the input or follow a non-word character. To keep the raw-text fall-back
// case the Supabase comment above describes, a literal JSON escape (\n, \t,
// ", ...) also counts as a boundary even though its last byte is a word
// character.
//
// The body must contain a digit or an uppercase letter. Real keys are random
// base64url, so an all-lowercase, digit-free 20+ char body is vanishingly
// unlikely to be a key, while kebab-case names such as
// "sk-learn-model-selection-guide" are exactly that shape.
var bareSKKeyPattern = regexp.MustCompile(`(?:^|[^A-Za-z0-9_]|\\[bfnrt]|\\u[0-9A-Fa-f]{4})(sk-[A-Za-z0-9_-]{20,})`)

// detectProviderTokens returns tagged regions for every occurrence of a
// known provider secret-token prefix in s. Regions use the empty label so
// they render as the bare "REDACTED" token, consistent with the other
// always-on secret layers.
func detectProviderTokens(s string) []taggedRegion {
	var regions []taggedRegion
	for _, pat := range providerTokenPatterns {
		for _, loc := range pat.FindAllStringIndex(s, -1) {
			regions = append(regions, taggedRegion{region: region{loc[0], loc[1]}})
		}
	}
	for _, loc := range bareSKKeyPattern.FindAllStringSubmatchIndex(s, -1) {
		start, end := loc[2], loc[3]
		if strings.ContainsFunc(s[start+len("sk-"):end], isDigitOrUpper) {
			regions = append(regions, taggedRegion{region: region{start, end}})
		}
	}
	return regions
}

func isDigitOrUpper(r rune) bool {
	return ('0' <= r && r <= '9') || ('A' <= r && r <= 'Z')
}
