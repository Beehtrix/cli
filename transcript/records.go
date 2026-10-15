package transcript

import "encoding/json"

// Transcript records are the append-only form of an Entire Transcript Format
// transcript. An agent-specific adapter reads a redacted agent transcript and
// emits records; it never revises a record it has emitted, so records can be
// streamed and stored as plain appends. The fold (Fold, Folder) is
// agent-agnostic: applying records in order produces the lines Convert writes.
// Claude Code, Cursor and Droid transcripts go through an adapter and the fold;
// the other converters still write lines directly. The CLI folds records when
// it writes a checkpoint, and the backend folds the same records to show a
// live session.

// Op names what a record does to its target message.
type Op string

const (
	// OpMessage creates a message, or replaces the role, timestamp, source ID
	// and content of a known one and restores it if it was dropped.
	OpMessage Op = "message"
	// OpAppend appends content blocks to a message; the record's timestamp
	// becomes the message's timestamp.
	OpAppend Op = "append"
	// OpResult sets the result of a tool_use block in a message.
	OpResult Op = "result"
	// OpUsage sets a message's token counts.
	OpUsage Op = "usage"
	// OpDrop hides a message.
	OpDrop Op = "drop"
)

// Record is one transcript record. Which fields apply depends on Op; Role is
// TypeUser or TypeAssistant.
//
// ID is the fold key. It is unique within a session and stable: the same
// transcript yields the same IDs on every run. SourceID is the agent's own ID
// for the message and is what the compact line prints as "id"; it is empty
// when the agent has none. The two differ when the adapter had to derive a key,
// for example when the agent reuses a message ID for a second, non-adjacent
// message that compaction keeps separate.
type Record struct {
	Op       Op     `json:"op"`
	ID       string `json:"id"`
	SourceID string `json:"sourceId,omitempty"`
	Role     string `json:"role,omitempty"`

	// TS is the agent's timestamp, passed through verbatim.
	TS json.RawMessage `json:"ts,omitempty"`
	// Content is a JSON array of compact content blocks. Adapters may pass a
	// non-array value through on OpMessage when the agent wrote one.
	Content json.RawMessage `json:"content,omitempty"`

	// ToolUseID selects the tool_use block an OpResult targets. Empty selects
	// the last tool_use block in the message.
	ToolUseID string      `json:"toolUseId,omitempty"`
	Result    *ToolResult `json:"result,omitempty"`

	// Token counts. A non-zero value replaces the message's previous value.
	InputTokens  int `json:"inputTokens,omitempty"`
	OutputTokens int `json:"outputTokens,omitempty"`
}
