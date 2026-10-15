package transcript

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// jsonlAdapter turns a Claude Code, Cursor or Droid JSONL transcript into
// transcript records. It is incremental: Feed accepts the transcript in
// chunks split anywhere, and the records depend only on the bytes fed, not
// on how they were chunked. Folding the records reproduces what compaction
// produced before it was split into adapter and fold.
//
// The compaction rules live here, as decisions about which message a record
// targets:
//   - Consecutive assistant entries with the same message ID are fragments of
//     one message: the first opens it, the rest append to it.
//   - Tool results in the user entry immediately after an assistant entry
//     become result records on that assistant message. Results elsewhere are
//     dropped, as is a user entry that held only such results.
//   - The text and images of that user entry become a separate user message.
//
// Fold keys are the agent's message ID where it has one and it is unused,
// otherwise derived from the source line number (1-based, counting every line
// fed, including blank and dropped ones). A message ID the agent reuses for a
// later, non-adjacent message gets "<id>@L<line>" as its key and keeps the
// agent's ID as SourceID; messages without an ID, including all user
// messages, get "@L<line>" and no SourceID. Keys are stable for a transcript
// fed from its first line.
type jsonlAdapter struct {
	preprocess linePreprocessor

	pending []byte          // bytes after the last newline fed
	line    int             // source lines consumed so far
	keys    map[string]bool // fold keys handed out
	open    *openAssistant  // set while the last parsed entry is an assistant
}

// openAssistant is the assistant message that further fragments, or the next
// user entry's tool results, still attach to.
//
// Merging fragment content costs time in the whole message, so it is deferred:
// parts holds each fragment's content, and entry.content is brought up to date
// only when the message has to be restated.
type openAssistant struct {
	key     string
	entry   parsedEntry       // fragments merged so far, except content
	parts   []json.RawMessage // content of each fragment since the last restatement
	isArray bool              // whether the merged content is a JSON array
}

func newJSONLAdapter(preprocess linePreprocessor) *jsonlAdapter {
	return &jsonlAdapter{preprocess: preprocess, keys: make(map[string]bool)}
}

// Feed consumes the next chunk of the transcript and returns the records
// implied by every line it completed. A trailing partial line is held until a
// later chunk completes it, or until Flush.
func (a *jsonlAdapter) Feed(chunk []byte) []Record {
	var out []Record
	for len(chunk) > 0 {
		i := bytes.IndexByte(chunk, '\n')
		if i < 0 {
			a.pending = append(a.pending, chunk...)
			break
		}
		lineBytes := chunk[:i+1]
		if len(a.pending) > 0 {
			a.pending = append(a.pending, lineBytes...)
			lineBytes = a.pending
		}
		out = a.consumeLine(lineBytes, out)
		a.pending = a.pending[:0]
		chunk = chunk[i+1:]
	}
	return out
}

// Flush treats a held partial line as complete. Call it only once the
// transcript is final; while an agent is still writing, a partial line is
// usually an unfinished write.
func (a *jsonlAdapter) Flush() []Record {
	if len(a.pending) == 0 {
		return nil
	}
	lineBytes := a.pending
	a.pending = nil
	return a.consumeLine(lineBytes, nil)
}

func (a *jsonlAdapter) consumeLine(lineBytes []byte, out []Record) []Record {
	a.line++
	if len(bytes.TrimSpace(lineBytes)) == 0 {
		return out
	}
	e, ok := parseLine(lineBytes, a.preprocess)
	if !ok {
		return out
	}
	switch e.kind {
	case TypeAssistant:
		return a.assistant(e, out)
	case TypeUser:
		return a.user(e, out)
	}
	return out
}

func (a *jsonlAdapter) assistant(e parsedEntry, out []Record) []Record {
	o := a.open
	if o == nil || o.entry.id != e.id {
		o = &openAssistant{key: a.newKey(e.id), entry: e, parts: []json.RawMessage{e.content}, isArray: isJSONArray(e.content)}
		a.open = o
		return append(out, o.message())
	}

	o.entry.ts = e.ts
	if e.inputTokens > 0 {
		o.entry.inputTokens = e.inputTokens
	}
	if e.outputTokens > 0 {
		o.entry.outputTokens = e.outputTokens
	}
	o.parts = append(o.parts, e.content)
	if !o.isArray || !isJSONArray(e.content) {
		// Compaction's merge treats a non-array side as contributing no
		// blocks and can yield null; restate the merged message rather than
		// rely on the fold's append agreeing.
		o.mergeParts()
		return append(out, o.message())
	}
	return append(out, Record{
		Op:           OpAppend,
		ID:           o.key,
		TS:           e.ts,
		Content:      e.content,
		InputTokens:  e.inputTokens,
		OutputTokens: e.outputTokens,
	})
}

func (a *jsonlAdapter) user(e parsedEntry, out []Record) []Record {
	o := a.open
	a.open = nil

	hasContent := e.userText != "" || len(e.userImages) > 0
	if hasToolResults(e) {
		if o != nil {
			for _, tr := range e.toolResults {
				out = append(out, Record{
					Op:        OpResult,
					ID:        o.key,
					ToolUseID: tr.toolUseID,
					Result:    toolResult(tr),
				})
			}
		}
		if !hasContent {
			return out
		}
	}

	content, err := userContentBlocks(e)
	if err != nil {
		return out
	}
	return append(out, Record{
		Op:      OpMessage,
		ID:      a.newKey(""),
		Role:    TypeUser,
		TS:      e.ts,
		Content: content,
	})
}

// newKey returns an unused fold key for a message starting on the current
// line, preferring the agent's own ID.
func (a *jsonlAdapter) newKey(sourceID string) string {
	key := sourceID
	if key == "" || a.keys[key] {
		key = fmt.Sprintf("%s@L%d", sourceID, a.line)
	}
	// Only an agent ID shaped like a derived key can collide here.
	for a.keys[key] {
		key += "'"
	}
	a.keys[key] = true
	return key
}

// mergeParts merges the pending fragment contents the way compaction merges
// fragments, one at a time, and keeps the result as the only part.
func (o *openAssistant) mergeParts() {
	merged := parsedEntry{content: o.parts[0]}
	for _, part := range o.parts[1:] {
		merged = mergeAssistantEntries(merged, parsedEntry{content: part})
	}
	o.entry.content = merged.content
	o.parts = []json.RawMessage{merged.content}
	o.isArray = isJSONArray(merged.content)
}

func (o *openAssistant) message() Record {
	return Record{
		Op:           OpMessage,
		ID:           o.key,
		SourceID:     o.entry.id,
		Role:         TypeAssistant,
		TS:           o.entry.ts,
		Content:      o.entry.content,
		InputTokens:  o.entry.inputTokens,
		OutputTokens: o.entry.outputTokens,
	}
}

// isJSONArray reports whether raw is a JSON array (not null).
func isJSONArray(raw json.RawMessage) bool {
	var arr []json.RawMessage
	return json.Unmarshal(raw, &arr) == nil && arr != nil
}
