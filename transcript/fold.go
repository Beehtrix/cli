package transcript

import (
	"encoding/json"
	"errors"
	"fmt"
)

type message struct {
	id           string
	sourceID     string
	role         string
	ts           json.RawMessage
	content      json.RawMessage
	inputTokens  int
	outputTokens int
	dropped      bool
}

// Folder applies records incrementally. Messages keep the order in which
// their IDs were first created. The zero value is not usable; call NewFolder.
type Folder struct {
	opts Options
	msgs []*message
	byID map[string]*message
}

// NewFolder returns an empty Folder whose lines carry opts.Agent and
// opts.CLIVersion; opts.StartLine is ignored.
func NewFolder(opts Options) *Folder {
	return &Folder{opts: opts, byID: make(map[string]*message)}
}

// Fold applies records to an empty Folder and returns its lines.
func Fold(records []Record, opts Options) ([]byte, error) {
	f := NewFolder(opts)
	if _, err := f.Apply(records); err != nil {
		return nil, err
	}
	return f.Lines(), nil
}

// Apply applies records in order and returns the IDs of the messages they
// touched, each once, in order of first touch. Apply validates every record
// before changing anything, so on error the Folder is unchanged.
func (f *Folder) Apply(records []Record) ([]string, error) {
	if err := f.validate(records); err != nil {
		return nil, err
	}

	var touched []string
	seen := make(map[string]bool)
	for _, r := range records {
		f.apply(r)
		if !seen[r.ID] {
			seen[r.ID] = true
			touched = append(touched, r.ID)
		}
	}
	return touched, nil
}

// Lines returns the compact transcript: one newline-terminated line per
// visible message, in creation order.
func (f *Folder) Lines() []byte {
	var out []byte
	for _, m := range f.msgs {
		if line, ok := f.line(m); ok {
			out = append(out, line...)
			out = append(out, '\n')
		}
	}
	return out
}

// Line returns the compact line (without a trailing newline) for message id.
// visible is false when the message is unknown, dropped, or an assistant
// message with no content yet; such a message has no line in Lines.
func (f *Folder) Line(id string) (line []byte, visible bool) {
	m, ok := f.byID[id]
	if !ok {
		return nil, false
	}
	return f.line(m)
}

func (f *Folder) line(m *message) ([]byte, bool) {
	if m.dropped || (m.role == TypeAssistant && isEmptyContentArray(m.content)) {
		return nil, false
	}
	line := newTranscriptLine(f.opts)
	line.Type = m.role
	line.TS = m.ts
	line.ID = m.sourceID
	line.InputTokens = m.inputTokens
	line.OutputTokens = m.outputTokens
	line.Content = m.content
	b, err := json.Marshal(line)
	if err != nil {
		return nil, false
	}
	return b, true
}

func (f *Folder) validate(records []Record) error {
	created := make(map[string]bool)
	for i, r := range records {
		if err := validateRecord(r); err != nil {
			return fmt.Errorf("record %d: %w", i, err)
		}
		if r.Op == OpMessage {
			created[r.ID] = true
			continue
		}
		if _, ok := f.byID[r.ID]; !ok && !created[r.ID] {
			return fmt.Errorf("record %d: %s targets unknown message %q", i, r.Op, r.ID)
		}
	}
	return nil
}

func validateRecord(r Record) error {
	if r.ID == "" {
		return errors.New("missing id")
	}
	if len(r.TS) > 0 && !json.Valid(r.TS) {
		return errors.New("ts is not valid JSON")
	}
	if len(r.Content) > 0 && !json.Valid(r.Content) {
		return errors.New("content is not valid JSON")
	}
	switch r.Op {
	case OpMessage:
		if r.Role != TypeUser && r.Role != TypeAssistant {
			return fmt.Errorf("message role %q is not %q or %q", r.Role, TypeUser, TypeAssistant)
		}
	case OpResult:
		if r.Result == nil {
			return errors.New("result record without result")
		}
	case OpAppend, OpUsage, OpDrop:
	default:
		return fmt.Errorf("unknown op %q", r.Op)
	}
	return nil
}

// apply applies a validated record.
func (f *Folder) apply(r Record) {
	m := f.byID[r.ID]
	switch r.Op {
	case OpMessage:
		if m == nil {
			m = &message{id: r.ID}
			f.byID[r.ID] = m
			f.msgs = append(f.msgs, m)
		}
		m.sourceID = r.SourceID
		m.role = r.Role
		m.ts = r.TS
		m.content = r.Content
		m.dropped = false
		m.setTokens(r)
	case OpAppend:
		m.ts = r.TS
		m.content = appendBlocks(m.content, r.Content)
		m.setTokens(r)
	case OpResult:
		m.content = setToolResult(m.content, r.ToolUseID, r.Result)
	case OpUsage:
		m.setTokens(r)
	case OpDrop:
		m.dropped = true
	}
}

// setTokens applies the record's non-zero token counts. Agents report
// cumulative usage, so the latest non-zero value is the total.
func (m *message) setTokens(r Record) {
	if r.InputTokens > 0 {
		m.inputTokens = r.InputTokens
	}
	if r.OutputTokens > 0 {
		m.outputTokens = r.OutputTokens
	}
}

// appendBlocks concatenates two content arrays. A value that is not an array
// contributes no blocks.
func appendBlocks(content, add json.RawMessage) json.RawMessage {
	var blocks, more []json.RawMessage
	if json.Unmarshal(content, &blocks) != nil {
		blocks = nil
	}
	if json.Unmarshal(add, &more) != nil {
		more = nil
	}
	all := make([]json.RawMessage, 0, len(blocks)+len(more))
	all = append(all, blocks...)
	all = append(all, more...)
	data, err := json.Marshal(all)
	if err != nil {
		return content
	}
	return data
}

// setToolResult sets result on the last tool_use block whose id is
// toolUseID, or on the last tool_use block when toolUseID is empty. Content
// that is not an array of blocks, or has no such block, is returned unchanged.
func setToolResult(content json.RawMessage, toolUseID string, result *ToolResult) json.RawMessage {
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(content, &blocks) != nil {
		return content
	}
	for i := len(blocks) - 1; i >= 0; i-- {
		if unquote(blocks[i]["type"]) != ContentTypeToolUse {
			continue
		}
		if toolUseID != "" && unquote(blocks[i]["id"]) != toolUseID {
			continue
		}
		resultJSON, err := json.Marshal(result)
		if err != nil {
			return content
		}
		blocks[i]["result"] = resultJSON
		data, err := json.Marshal(blocks)
		if err != nil {
			return content
		}
		return data
	}
	return content
}
