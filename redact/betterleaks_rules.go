package redact

import (
	"context"
	"fmt"

	"github.com/betterleaks/betterleaks/config"
	"github.com/betterleaks/betterleaks/detect"
	"github.com/betterleaks/betterleaks/report"
)

// betterleaksSecretComponents lists the betterleaks component-only rules
// (SkipReport) whose matches are credentials in their own right. Two things
// hide them by default, and both must be undone (#2808):
//
//   - SkipReport: the rule never reports standalone, so a secret without its
//     partner nearby produced no finding. Un-skipping fixes that.
//   - Component suppression: when the composite does fire, betterleaks drops
//     every other finding on the component's span (generic-api-key included)
//     and reports the secret only inside Finding.ComponentSets. So
//     betterleaksFindingSecrets must read component sets too; otherwise the
//     AWS secret access key survived beside its key ID whenever the entropy
//     layer missed it.
//
// The remaining component-only rules match identifiers, hosts, URLs, and
// names (client IDs, tenant IDs, *.jfrog.io, usernames) and stay skipped.
// TestBetterleaksComponentRulesClassified fails when an upgrade adds an
// unclassified one.
var betterleaksSecretComponents = map[string]bool{
	"aws-secret-access-key":      true,
	"alibaba-access-key-id":      true,
	"alibaba-sts-access-key-id":  true,
	"alibaba-sts-security-token": true,
	"exoscale-api-secret":        true,
	"ovh-consumer-key":           true,
	"polymarket-api-secret":      true,
	"polymarket-passphrase":      true,
}

// newBetterleaksDetector builds the betterleaks detector from the default
// ruleset, adjusted for redaction rather than leak reporting.
//
// betterleaks composite rules require their components nearby (an Azure client
// secret needs a tenant ID and client ID within eight lines), which keeps its
// validation and reports precise. Redaction must not depend on what else the
// transcript happens to contain, so every component becomes optional: the
// primary secret is reported alone, and components are still attached when
// found. Filters never read components, only validation expressions do, and
// this package does not validate.
func newBetterleaksDetector() (*detect.Detector, error) {
	cfg, err := config.Default()
	if err != nil {
		return nil, fmt.Errorf("load betterleaks default config: %w", err)
	}
	for id, rule := range cfg.Rules {
		for _, component := range rule.Components {
			component.Optional = true
		}
		if rule.SkipReport && betterleaksSecretComponents[id] {
			rule.SkipReport = false
			cfg.Rules[id] = rule
		}
	}
	return detect.NewDetectorContext(context.Background(), cfg, detect.ValidationOptions{}), nil
}

// betterleaksFindingSecrets returns the values to redact for one finding: its
// own secret plus every credential-class component attached to it.
func betterleaksFindingSecrets(f report.Finding) []string {
	secrets := []string{f.Secret}
	for _, set := range f.ComponentSets {
		for _, component := range set.Components {
			if betterleaksSecretComponents[component.RuleID] && component.Secret != "" {
				secrets = append(secrets, component.Secret)
			}
		}
	}
	return secrets
}
