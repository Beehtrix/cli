package transcript

import "encoding/json"

// codexUsage is the cumulative total_token_usage reported by a token_count
// event. Reasoning is nil when the snapshot does not report it.
type codexUsage struct {
	Input     int64
	Cached    int64
	Output    int64
	Reasoning *int64
}

// codexCallUsage is the usage of one model call: the difference between two
// cumulative snapshots. Input excludes cache reads.
type codexCallUsage struct {
	Model     string
	Input     int64
	CacheRead int64
	Output    int64
	Reasoning int64
}

type codexUsageTracker struct {
	previous      codexUsage
	lastReasoning int64
	halted        bool
}

func newCodexUsageTracker() *codexUsageTracker {
	return &codexUsageTracker{previous: codexUsage{Reasoning: new(int64)}}
}

func (t *codexUsageTracker) stopped() bool { return t.halted }

// next returns the usage of the call that ended at total. ok is false for a
// repeated snapshot (no model call; the baseline is kept) and once a counter
// decrease has stopped collection.
func (t *codexUsageTracker) next(total codexUsage) (d codexCallUsage, ok bool) {
	if t.halted {
		return codexCallUsage{}, false
	}
	prev := t.previous
	if total.Input < prev.Input || total.Cached < prev.Cached || total.Output < prev.Output ||
		(total.Reasoning != nil && *total.Reasoning < t.lastReasoning) {
		t.halted = true
		return codexCallUsage{}, false
	}
	// Rate-limit refreshes repeat totals without a model call.
	if total.Input == prev.Input && total.Cached == prev.Cached && total.Output == prev.Output {
		return codexCallUsage{}, false
	}
	d = codexCallUsage{
		Input:     max(int64(0), (total.Input-prev.Input)-(total.Cached-prev.Cached)),
		CacheRead: total.Cached - prev.Cached,
		Output:    total.Output - prev.Output,
	}
	// Only adjacent snapshots can establish reasoning usage across a gap.
	if total.Reasoning != nil && prev.Reasoning != nil {
		d.Reasoning = *total.Reasoning - *prev.Reasoning
	}
	if total.Reasoning != nil {
		t.lastReasoning = *total.Reasoning
	}
	t.previous = total
	return d, true
}

// codexTokenCountTotal extracts total_token_usage from a token_count payload.
// ok is false for any other payload, or when the cumulative total is absent or
// lacks the input, cached-input or output counter.
func codexTokenCountTotal(payload json.RawMessage) (total codexUsage, ok bool) {
	var event struct {
		Type string `json:"type"`
		Info *struct {
			Total *struct {
				Input     *int64 `json:"input_tokens"`
				Cached    *int64 `json:"cached_input_tokens"`
				Output    *int64 `json:"output_tokens"`
				Reasoning *int64 `json:"reasoning_output_tokens"`
			} `json:"total_token_usage"`
		} `json:"info"`
	}
	if json.Unmarshal(payload, &event) != nil || event.Type != "token_count" || event.Info == nil || event.Info.Total == nil {
		return codexUsage{}, false
	}
	t := event.Info.Total
	if t.Input == nil || t.Cached == nil || t.Output == nil {
		return codexUsage{}, false
	}
	return codexUsage{Input: *t.Input, Cached: *t.Cached, Output: *t.Output, Reasoning: t.Reasoning}, true
}
