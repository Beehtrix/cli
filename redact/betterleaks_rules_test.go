package redact

import (
	"slices"
	"strings"
	"testing"

	"github.com/betterleaks/betterleaks/config"
	"github.com/betterleaks/betterleaks/report"
)

// betterleaksNonSecretComponents lists the component-only rules that match
// identifiers, hosts, URLs, and names rather than credentials. Together with
// betterleaksSecretComponents it must cover every SkipReport rule.
var betterleaksNonSecretComponents = []string{
	"aikido-client-id",
	"algolia-application-id",
	"artifactory-jfrog-url",
	"auth0-client-id.1",
	"auth0-domain.1",
	"azure-client-id",
	"azure-storage-account-name",
	"azure-tenant-id",
	"browserstack-username.1",
	"canva-client-id",
	"clickhouse-cloud-key-id",
	"cloudinary-api-key",
	"cloudinary-cloud-name",
	"ebay-client-id",
	"generic-username",
	"langfuse-public-key.1",
	"lark-app-id",
	"miro-client-id",
	"mongodb-atlas-service-account-id",
	"mux-access-token-id.1",
	"ovh-application-key",
	"paypal-client-id.1",
	"planetscale-id",
	"plivo-auth-id",
	"polymarket-address",
	"ramp-client-id",
	"razorpay-key-id.1",
	"salesforce-instance-url.1",
	"snowflake-account-host.1",
	"supabase-project-url",
	"tableau-personal-access-token-name.1",
	"tableau-server-host.1",
	"upstash-redis-rest-url.1",
	"wiz-client-id.1",
	"zoho-client-id.1",
}

// TestBetterleaksComponentRulesClassified fails when a betterleaks upgrade adds,
// renames, or un-skips a component-only rule, so each one is classified as a
// credential (redacted) or an identifier (left visible) on purpose.
func TestBetterleaksComponentRulesClassified(t *testing.T) {
	t.Parallel()

	cfg, err := config.Default()
	if err != nil {
		t.Fatalf("load betterleaks default config: %v", err)
	}
	for id, rule := range cfg.Rules {
		if !rule.SkipReport {
			continue
		}
		secret := betterleaksSecretComponents[id]
		nonSecret := slices.Contains(betterleaksNonSecretComponents, id)
		if secret == nonSecret {
			t.Errorf("component-only rule %q must be in exactly one of betterleaksSecretComponents and betterleaksNonSecretComponents", id)
		}
	}
	for id := range betterleaksSecretComponents {
		if rule, ok := cfg.Rules[id]; !ok || !rule.SkipReport {
			t.Errorf("betterleaksSecretComponents lists %q, which is no longer a component-only rule", id)
		}
	}
	for _, id := range betterleaksNonSecretComponents {
		if rule, ok := cfg.Rules[id]; !ok || !rule.SkipReport {
			t.Errorf("betterleaksNonSecretComponents lists %q, which is no longer a component-only rule", id)
		}
	}
}

// awsSecretKeyFixture is a 40-char secret access key containing "/", which
// splits it into chunks the entropy layer misses. Assembled like awsKeyFixture.
const awsSecretKeyFixture = "Qx7/Lm2PvT9kWz+R4n" + "Bc/Hy6JdF8sGa3Ue5oKi1N"

// azureClientSecretFixture has the Azure AD client secret shape: "~" and "."
// split it for the entropy layer, and betterleaks required a tenant ID and a
// client ID within eight lines before reporting it.
const azureClientSecretFixture = "xR48Q~" + "kT2.mW9vLp4nZq7sYb3cJf6hDg1aEo5uX"

// TestString_AWSCredentialsPairRedactsBoth covers #2808's worse half: with the
// key ID present the composite rule fires, and betterleaks suppresses every
// other finding on the secret (generic-api-key included) in favour of the
// component it attaches, so the secret survived unless read from ComponentSets.
func TestString_AWSCredentialsPairRedactsBoth(t *testing.T) {
	t.Parallel()

	input := "[default]\naws_access_key_id = " + awsKeyFixture + "\naws_secret_access_key = " + awsSecretKeyFixture + "\n"
	want := "[default]\naws_access_key_id = REDACTED\naws_secret_access_key = REDACTED\n"
	if got := String(input); got != want {
		t.Errorf("String(%q) = %q, want %q", input, got, want)
	}
}

// TestBetterleaksDetector_ReportsLoneSecrets pins the two detector adjustments
// directly: a component-only credential and a composite primary each produce a
// finding without their partners.
func TestBetterleaksDetector_ReportsLoneSecrets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		ruleID string
		secret string
	}{
		{
			name:   "AWS secret access key without its key ID",
			input:  "aws_secret_access_key = " + awsSecretKeyFixture,
			ruleID: "aws-secret-access-key",
			secret: awsSecretKeyFixture,
		},
		{
			name:   "Azure AD client secret without tenant and client IDs",
			input:  "AZURE_CLIENT_SECRET=" + azureClientSecretFixture,
			ruleID: "azure-ad-client-secret",
			secret: azureClientSecretFixture,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			findings := getDetector().DetectString(tt.input)
			if !slices.ContainsFunc(findings, func(f report.Finding) bool { return f.RuleID == tt.ruleID && f.Secret == tt.secret }) {
				t.Errorf("DetectString(%q) has no %s finding for the secret; got %v", tt.input, tt.ruleID, findings)
			}
			if got := String(tt.input); strings.Contains(got, tt.secret) {
				t.Errorf("String(%q) = %q, secret survived", tt.input, got)
			}
		})
	}
}
