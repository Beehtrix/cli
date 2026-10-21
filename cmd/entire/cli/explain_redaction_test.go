package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/entireio/cli/redact"

	"github.com/stretchr/testify/require"
)

func headerWithRedaction(summary *checkpoint.CheckpointSummary, counts *redact.Counts) string {
	meta := checkpoint.Metadata{
		SessionID: "s",
		CreatedAt: time.Date(2026, 4, 29, 14, 22, 8, 0, time.UTC),
		Redaction: counts,
	}
	return formatCheckpointHeader(summary, meta, id.MustCheckpointID("a3b2c4d5e6f7"), nil, checkpoint.Author{}, statusStyles{colorEnabled: false, width: 60})
}

func TestFormatCheckpointHeader_RedactionRow(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		counts *redact.Counts
		want   string
	}{
		{
			name: "matches listed in detection order, zero layers left out",
			counts: &redact.Counts{
				Replacements:      14,
				Layers:            map[string]int{"pii": 3, "custom": 2, "betterleaks": 3, "entropy": 9, "provider_token": 0},
				CustomRulesActive: 2,
			},
			want: "  redacted 14 replacements · matches: entropy 9, betterleaks 3, custom 2 (2 rules), pii 3\n",
		},
		{
			// Custom rules that ran and matched nothing stay visible, which is
			// how a reader tells them apart from rules that never loaded.
			name:   "custom rules that matched nothing",
			counts: &redact.Counts{Replacements: 1, Layers: map[string]int{"entropy": 1, "custom": 0}, CustomRulesActive: 3},
			want:   "  redacted 1 replacement · matches: entropy 1, custom 0 (3 rules)\n",
		},
		{
			name:   "nothing redacted",
			counts: &redact.Counts{Layers: map[string]int{"entropy": 0}},
			want:   "  redacted 0 replacements\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Contains(t, headerWithRedaction(nil, tc.counts), tc.want)
		})
	}
}

func TestFormatCheckpointHeader_RedactionRowAbsentWithoutCounts(t *testing.T) {
	t.Parallel()
	require.NotContains(t, headerWithRedaction(nil, nil), "redacted")
}

func TestFormatCheckpointHeader_RedactionFallsBackToCheckpointTotal(t *testing.T) {
	t.Parallel()
	summary := &checkpoint.CheckpointSummary{Redaction: &redact.Counts{Replacements: 2, Layers: map[string]int{"entropy": 2}}}
	require.Contains(t, headerWithRedaction(summary, nil), "  redacted 2 replacements · matches: entropy 2\n")
}

func TestBuildCheckpointJSONEnvelope_RedactionCounts(t *testing.T) {
	t.Parallel()

	cpID := id.MustCheckpointID("dddd77776666")
	sessionCounts := &redact.Counts{Replacements: 2, Layers: map[string]int{"entropy": 2, "custom": 0}, CustomRulesActive: 1}
	summary := &checkpoint.CheckpointSummary{
		Strategy:  "manual-commit",
		Sessions:  []checkpoint.SessionFilePaths{{}, {}},
		Redaction: sessionCounts,
	}
	reader := &stubCommittedReader{
		summary: summary,
		contents: map[int]*checkpoint.SessionContent{
			0: {Metadata: checkpoint.Metadata{SessionID: "counted", Redaction: sessionCounts}},
			1: {Metadata: checkpoint.Metadata{SessionID: "older-cli"}},
		},
	}

	envelope, failed := buildCheckpointJSONEnvelope(context.Background(), reader, summary, cpID)
	require.Empty(t, failed)
	require.Equal(t, sessionCounts, envelope.Redaction)
	require.Equal(t, sessionCounts, envelope.Sessions[0].Redaction)
	require.Nil(t, envelope.Sessions[1].Redaction)

	data, err := json.Marshal(envelope)
	require.NoError(t, err)
	require.Contains(t, string(data), `"redaction":{"replacements":2,"layers":{"custom":0,"entropy":2},"custom_rules_active":1}`)
	require.Equal(t, 2, strings.Count(string(data), `"redaction"`), "a session without counts carries no redaction key")
}
