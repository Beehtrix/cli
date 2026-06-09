package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint"
	"github.com/entireio/cli/cmd/entire/cli/checkpoint/id"
	"github.com/spf13/cobra"
)

type checkpointTokensReport struct {
	CheckpointID    string                        `json:"checkpoint_id"`
	SessionCount    int                           `json:"session_count"`
	SessionID       string                        `json:"session_id,omitempty"`
	Agent           string                        `json:"agent,omitempty"`
	Agents          []string                      `json:"agents,omitempty"`
	Model           string                        `json:"model,omitempty"`
	Models          []string                      `json:"models,omitempty"`
	Branch          string                        `json:"branch,omitempty"`
	Source          string                        `json:"source"`
	Tokens          *sessionTokensUsage           `json:"tokens,omitempty"`
	Context         *sessionTokensContext         `json:"context,omitempty"`
	Contributors    []sessionTokensContributor    `json:"contributors,omitempty"`
	Recommendations []sessionTokensRecommendation `json:"recommendations,omitempty"`
	Limitations     []string                      `json:"limitations,omitempty"`
}

func newCheckpointTokensCmd() *cobra.Command {
	var jsonFlag bool

	cmd := &cobra.Command{
		Use:   "tokens <checkpoint-id>",
		Short: "Show token usage and optimization recommendations for a checkpoint",
		Long: `Show token usage and optimization recommendations for a checkpoint.

The report reads committed checkpoint metadata and is local and deterministic.
Checkpoint IDs may be abbreviated as long as the prefix is unambiguous.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCheckpointTokens(cmd.Context(), cmd, args[0], jsonFlag)
		},
	}

	cmd.Flags().BoolVar(&jsonFlag, "json", false, "Output as JSON")
	return cmd
}

func runCheckpointTokens(ctx context.Context, cmd *cobra.Command, checkpointIDPrefix string, jsonOutput bool) error {
	cpID, lookup, err := resolveExplainCheckpointID(ctx, cmd.ErrOrStderr(), explainExportOptions{target: checkpointIDPrefix})
	if lookup != nil {
		defer lookup.Close()
	}
	if err != nil {
		return err
	}

	summary, err := lookup.store.ReadCommitted(ctx, cpID)
	if err != nil {
		return fmt.Errorf("failed to read checkpoint: %w", err)
	}
	if summary == nil || len(summary.Sessions) == 0 {
		cmd.SilenceUsage = true
		fmt.Fprintln(cmd.ErrOrStderr(), "Checkpoint not found.")
		return NewSilentError(fmt.Errorf("%w: %s", checkpoint.ErrCheckpointNotFound, checkpointIDPrefix))
	}

	metas := make([]*checkpoint.CommittedMetadata, 0, len(summary.Sessions))
	for i := range len(summary.Sessions) {
		meta, err := lookup.store.ReadSessionMetadata(ctx, cpID, i)
		if err != nil {
			return fmt.Errorf("failed to read checkpoint session %d metadata: %w", i, err)
		}
		metas = append(metas, meta)
	}

	report := buildCheckpointTokensReport(cpID, summary, metas)
	if jsonOutput {
		return writeCheckpointTokensJSON(cmd.OutOrStdout(), report)
	}
	writeCheckpointTokensText(cmd.OutOrStdout(), report)
	return nil
}

func buildCheckpointTokensReport(cpID id.CheckpointID, summary *checkpoint.CheckpointSummary, metas []*checkpoint.CommittedMetadata) checkpointTokensReport {
	report := checkpointTokensReport{
		CheckpointID: cpID.String(),
		Source:       "committed_checkpoint",
	}
	if summary != nil {
		report.Branch = summary.Branch
		report.SessionCount = len(summary.Sessions)
	}
	if report.SessionCount == 0 {
		report.SessionCount = len(metas)
	}
	report.Agents = checkpointAgentLabels(metas)
	report.Models = checkpointModelLabels(metas)

	if len(metas) == 1 && metas[0] != nil {
		meta := metas[0]
		report.SessionID = meta.SessionID
		if len(report.Agents) > 0 {
			report.Agent = report.Agents[0]
		}
		if len(report.Models) > 0 {
			report.Model = report.Models[0]
		}
		if report.Branch == "" {
			report.Branch = meta.Branch
		}
	} else if len(metas) > 1 && report.Branch == "" {
		report.Branch = firstCheckpointBranch(metas)
	}

	usage := aggregateCheckpointTokenUsage(metas)
	if usage == nil && summary != nil {
		usage = summary.TokenUsage
	}
	if tokens := buildSessionTokensUsage(usage); tokens != nil {
		report.Tokens = tokens
		if tokens.SubagentTotal > 0 {
			report.Contributors = append(report.Contributors, sessionTokensContributor{
				Kind:       "subagents",
				Label:      "Subagents",
				Tokens:     tokens.SubagentTotal,
				Confidence: "reported",
				Signals:    []string{"subagent_tokens"},
			})
		}
	} else {
		report.Limitations = append(report.Limitations, "No token usage recorded for this checkpoint.")
		report.Recommendations = append(report.Recommendations, sessionTokensRecommendation{
			ID:       "no-token-data",
			Severity: "low",
			Message:  "Token usage is unavailable for this checkpoint; the agent may not expose token data yet, or this checkpoint predates token tracking.",
			Signals:  []string{"missing_token_usage"},
		})
	}

	var turnCount int
	var skillEvents []agent.SkillEvent
	if len(metas) == 1 && metas[0] != nil {
		meta := metas[0]
		if metrics := meta.SessionMetrics; metrics != nil {
			turnCount = metrics.TurnCount
			if contextInfo := buildSessionTokensContext(metrics.ContextTokens, metrics.ContextWindowSize); contextInfo != nil {
				report.Context = contextInfo
				report.Contributors = append(report.Contributors, sessionTokensContributor{
					Kind:       "context_pressure",
					Label:      "Context pressure",
					Percent:    contextInfo.Percent,
					Confidence: "reported",
					Signals:    []string{"context_tokens"},
				})
			}
		}
		skillEvents = meta.SkillEvents
	} else {
		for _, meta := range metas {
			if meta == nil {
				continue
			}
			if metrics := meta.SessionMetrics; metrics != nil {
				turnCount += metrics.TurnCount
			}
			skillEvents = append(skillEvents, meta.SkillEvents...)
		}
	}
	if labels := skillEventLabels(skillEvents); len(labels) > 0 {
		report.Contributors = append(report.Contributors, sessionTokensContributor{
			Kind:       "skills",
			Label:      "Skills/slash commands: " + strings.Join(labels, ", "),
			Confidence: "reported",
			Signals:    []string{"skill_events"},
		})
	}

	var checkpointCount int
	if summary != nil {
		checkpointCount = summary.CheckpointsCount
	}
	report.Recommendations = append(report.Recommendations, recommendationRules(tokenRecommendationSignals{
		Tokens:          report.Tokens,
		Context:         report.Context,
		TurnCount:       turnCount,
		CheckpointCount: checkpointCount,
	})...)
	return report
}

func checkpointAgentLabels(metas []*checkpoint.CommittedMetadata) []string {
	labels := make([]string, 0, len(metas))
	seen := make(map[string]struct{}, len(metas))
	for _, meta := range metas {
		label := unknownPlaceholder
		if meta != nil && meta.Agent != "" {
			label = string(meta.Agent)
		}
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		labels = append(labels, label)
	}
	return labels
}

func checkpointModelLabels(metas []*checkpoint.CommittedMetadata) []string {
	labels := make([]string, 0, len(metas))
	seen := make(map[string]struct{}, len(metas))
	for _, meta := range metas {
		if meta == nil || meta.Model == "" {
			continue
		}
		if _, ok := seen[meta.Model]; ok {
			continue
		}
		seen[meta.Model] = struct{}{}
		labels = append(labels, meta.Model)
	}
	return labels
}

func firstCheckpointBranch(metas []*checkpoint.CommittedMetadata) string {
	for _, meta := range metas {
		if meta != nil && meta.Branch != "" {
			return meta.Branch
		}
	}
	return ""
}

func aggregateCheckpointTokenUsage(metas []*checkpoint.CommittedMetadata) *agent.TokenUsage {
	var total *agent.TokenUsage
	for _, meta := range metas {
		if meta == nil {
			continue
		}
		total = addCheckpointTokenUsage(total, meta.TokenUsage)
	}
	return total
}

func addCheckpointTokenUsage(a, b *agent.TokenUsage) *agent.TokenUsage {
	if a == nil && b == nil {
		return nil
	}
	result := &agent.TokenUsage{}
	if a != nil {
		result.InputTokens = a.InputTokens
		result.CacheCreationTokens = a.CacheCreationTokens
		result.CacheReadTokens = a.CacheReadTokens
		result.OutputTokens = a.OutputTokens
		result.APICallCount = a.APICallCount
	}
	if b != nil {
		result.InputTokens += b.InputTokens
		result.CacheCreationTokens += b.CacheCreationTokens
		result.CacheReadTokens += b.CacheReadTokens
		result.OutputTokens += b.OutputTokens
		result.APICallCount += b.APICallCount
	}
	result.SubagentTokens = addCheckpointTokenUsage(tokenUsageSubagents(a), tokenUsageSubagents(b))
	return result
}

func tokenUsageSubagents(usage *agent.TokenUsage) *agent.TokenUsage {
	if usage == nil {
		return nil
	}
	return usage.SubagentTokens
}

func writeCheckpointTokensJSON(w io.Writer, report checkpointTokensReport) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(report); err != nil {
		return fmt.Errorf("failed to encode checkpoint token report: %w", err)
	}
	return nil
}

func writeCheckpointTokensText(w io.Writer, report checkpointTokensReport) {
	fmt.Fprintln(w, "Checkpoint tokens")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "Checkpoint: %s\n", report.CheckpointID)
	switch {
	case report.SessionCount > 1:
		fmt.Fprintf(w, "Sessions:   %d\n", report.SessionCount)
		if len(report.Agents) > 0 {
			fmt.Fprintf(w, "Agents:     %s\n", strings.Join(report.Agents, ", "))
		}
		if len(report.Models) > 0 {
			fmt.Fprintf(w, "Models:     %s\n", strings.Join(report.Models, ", "))
		}
	case report.SessionID != "":
		fmt.Fprintf(w, "Session:    %s\n", report.SessionID)
		if report.Agent != "" {
			fmt.Fprintf(w, "Agent:      %s\n", report.Agent)
		}
		if report.Model != "" {
			fmt.Fprintf(w, "Model:      %s\n", report.Model)
		}
	case report.Agent != "":
		fmt.Fprintf(w, "Agent:      %s\n", report.Agent)
	}
	if report.Branch != "" {
		fmt.Fprintf(w, "Branch:     %s\n", report.Branch)
	}

	writeTokenUsageSection(w, report.Tokens)
	if len(report.Recommendations) > 0 {
		writeTokenRecommendations(w, report.Recommendations)
	}
	writeTokenContributors(w, report.Contributors, report.Context)
	writeTokenLimitations(w, report.Limitations)
}
