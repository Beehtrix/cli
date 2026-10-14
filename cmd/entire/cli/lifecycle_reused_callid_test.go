package cli

import (
	"context"
	"testing"
	"time"

	"github.com/entireio/cli/cmd/entire/cli/agent"
	"github.com/entireio/cli/cmd/entire/cli/session"
	"github.com/entireio/cli/cmd/entire/cli/strategy"
	"github.com/entireio/cli/cmd/entire/cli/validation"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCompletionRecordKey pins how a completion-without-launch stop picks its
// record when its tool_use_id may be reused: some providers number tool calls
// per conversation, so OpenCode can send a later, different task call under
// a callID an earlier call's record already holds.
func TestCompletionRecordKey(t *testing.T) {
	t.Parallel()
	t1 := time.UnixMilli(1_000)
	t2 := time.UnixMilli(2_000)
	t3 := time.UnixMilli(3_000)
	done := time.UnixMilli(9_000)
	completed := func(key, child string, startedAt time.Time) session.TaskRecord {
		return session.TaskRecord{ToolUseID: key, AgentID: child, StartedAt: startedAt, CompletedAt: done}
	}

	tests := []struct {
		name      string
		records   []session.TaskRecord
		child     string
		startedAt time.Time
		want      string
	}{
		{
			name:  "no record under the callID keeps it",
			child: "ses_a", startedAt: t1,
			want: "c",
		},
		{
			name:    "a different call's record is not a reuse",
			records: []session.TaskRecord{completed("other", "ses_a", t1)},
			child:   "ses_b", startedAt: t2,
			want: "c",
		},
		{
			name:    "duplicate stop of the first call",
			records: []session.TaskRecord{completed("c", "ses_a", t1)},
			child:   "ses_a", startedAt: t1,
			want: "c",
		},
		{
			name:    "duplicate stop without a start time",
			records: []session.TaskRecord{completed("c", "ses_a", t1)},
			child:   "ses_a",
			want:    "c",
		},
		{
			name:    "live record of the same child is completed",
			records: []session.TaskRecord{{ToolUseID: "c", AgentID: "ses_a", StartedAt: t1}},
			child:   "ses_a", startedAt: t2,
			want: "c",
		},
		{
			name:    "a different child reusing the callID gets its own key",
			records: []session.TaskRecord{completed("c", "ses_a", t1)},
			child:   "ses_b", startedAt: t2,
			want: "c-ses_b",
		},
		{
			name:    "a different child while the first call is still live",
			records: []session.TaskRecord{{ToolUseID: "c", AgentID: "ses_a", StartedAt: t1}},
			child:   "ses_b", startedAt: t2,
			want: "c-ses_b",
		},
		{
			name:    "duplicate stop of the second call",
			records: []session.TaskRecord{completed("c", "ses_a", t1), completed("c-ses_b", "ses_b", t2)},
			child:   "ses_b", startedAt: t2,
			want: "c-ses_b",
		},
		{
			name:    "a later call resuming the same child gets its own key",
			records: []session.TaskRecord{completed("c", "ses_a", t1)},
			child:   "ses_a", startedAt: t2,
			want: "c-ses_a-2000",
		},
		{
			name:    "a stop starting no later than the completed call is its duplicate",
			records: []session.TaskRecord{completed("c", "ses_a", t2)},
			child:   "ses_a", startedAt: t1,
			want: "c",
		},
		{
			name:    "a third call resuming the second call's child",
			records: []session.TaskRecord{completed("c", "ses_a", t1), completed("c-ses_b", "ses_b", t2)},
			child:   "ses_b", startedAt: t3,
			want: "c-ses_b-3000",
		},
		{
			name: "duplicate stop of that third call",
			records: []session.TaskRecord{
				completed("c", "ses_a", t1), completed("c-ses_b", "ses_b", t2), completed("c-ses_b-3000", "ses_b", t3),
			},
			child: "ses_b", startedAt: t3,
			want: "c-ses_b-3000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state := &strategy.SessionState{TaskRecords: tt.records}
			event := &agent.Event{ToolUseID: "c", SubagentID: tt.child, SubagentStartedAt: tt.startedAt, CompletionWithoutLaunch: true}
			assert.Equal(t, tt.want, completionRecordKey(state, event))
		})
	}
}

// TestCompletionRecordKey_UnsafeChildHashesTheSuffix pins that a child ID the
// checkpoint path validator rejects still yields a path-safe, deterministic key.
func TestCompletionRecordKey_UnsafeChildHashesTheSuffix(t *testing.T) {
	t.Parallel()
	state := &strategy.SessionState{TaskRecords: []session.TaskRecord{
		{ToolUseID: "c", AgentID: "ses_a", StartedAt: time.UnixMilli(1_000), CompletedAt: time.UnixMilli(2_000)},
	}}
	event := &agent.Event{ToolUseID: "c", SubagentID: "ses.b:1", SubagentStartedAt: time.UnixMilli(3_000), CompletionWithoutLaunch: true}
	key := completionRecordKey(state, event)
	require.NoError(t, validation.ValidateToolUseID(key))
	assert.Regexp(t, `^c-[0-9a-f]{12}$`, key)
	assert.Equal(t, key, completionRecordKey(state, event), "repeated stops of one call must map alike")
}

// TestHandleLifecycleSubagentEnd_ReusedCallIDRecordsEachCall pins the stop
// side end to end: a second task call reusing the first one's callID is
// captured as its own record with its own transcript and files, and repeated
// stops of either call add nothing.
func TestHandleLifecycleSubagentEnd_ReusedCallIDRecordsEachCall(t *testing.T) {
	// NOT parallel: setupSubagentEndTestRepo uses t.Chdir.
	_, headHash := setupSubagentEndTestRepo(t)
	ctx := context.Background()
	const sessionID = "opencode-reused-callid"
	saveInFlightSession(ctx, t, sessionID, headHash)

	stop := func(child string, startedAt int64, files []string) {
		t.Helper()
		mainPath, childPath := writeDeclaredOnlyChildTranscript(t)
		event := finalSubagentEvent(sessionID, "call_0", child)
		event.SessionRef = mainPath
		event.SubagentTranscriptPath = childPath
		event.SubagentStartedAt = time.UnixMilli(startedAt)
		event.CompletionWithoutLaunch = true
		ag := &mockAnalyzerAgent{mockLifecycleAgent: newMockAgent(), analyzerFiles: files}
		require.NoError(t, handleLifecycleSubagentEnd(ctx, ag, event))
	}

	stop("ses_a", 1_000, []string{"a.md"})
	stop("ses_b", 2_000, []string{"b.md"})
	stop("ses_b", 2_000, []string{"b.md"})
	stop("ses_a", 1_000, []string{"a.md"})

	state, err := strategy.LoadSessionState(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, state.TaskRecords, 2, "each call gets one record; repeated stops add none")
	first := state.FindTaskRecord("call_0")
	require.NotNil(t, first)
	assert.Equal(t, "ses_a", first.AgentID)
	assert.Equal(t, []string{"a.md"}, first.Files)
	second := state.FindTaskRecord("call_0-ses_b")
	require.NotNil(t, second, "the reused callID's second call must not be dropped")
	assert.False(t, second.CompletedAt.IsZero())
	assert.Equal(t, "ses_b", second.AgentID)
	assert.Equal(t, []string{"b.md"}, second.Files)
	assert.NotEqual(t, first.DeclaredTranscriptPath, second.DeclaredTranscriptPath)
	assert.ElementsMatch(t, []string{"a.md", "b.md"}, state.FilesTouched)
}

// TestHandleLifecycleSubagentEnd_ReusedCallIDLeavesTheLiveCallAlone pins that
// a stop reusing the callID of a call still in flight (a background task) is
// recorded separately rather than completing that call's record with the
// wrong child.
func TestHandleLifecycleSubagentEnd_ReusedCallIDLeavesTheLiveCallAlone(t *testing.T) {
	// NOT parallel: setupSubagentEndTestRepo uses t.Chdir.
	_, headHash := setupSubagentEndTestRepo(t)
	ctx := context.Background()
	const sessionID = "opencode-reused-callid-live"
	saveInFlightSession(ctx, t, sessionID, headHash, session.TaskRecord{
		ToolUseID: "call_0", AgentID: "ses_a", StartedAt: time.UnixMilli(1_000), Background: true,
	})

	mainPath, childPath := writeDeclaredOnlyChildTranscript(t)
	event := finalSubagentEvent(sessionID, "call_0", "ses_b")
	event.SessionRef = mainPath
	event.SubagentTranscriptPath = childPath
	event.SubagentStartedAt = time.UnixMilli(2_000)
	event.CompletionWithoutLaunch = true
	require.NoError(t, handleLifecycleSubagentEnd(ctx, newMockAgent(), event))

	state, err := strategy.LoadSessionState(ctx, sessionID)
	require.NoError(t, err)
	live := state.FindTaskRecord("call_0")
	require.NotNil(t, live)
	assert.True(t, live.CompletedAt.IsZero(), "the first call is still running")
	assert.Equal(t, "ses_a", live.AgentID)
	assert.Empty(t, live.DeclaredTranscriptPath)
	second := state.FindTaskRecord("call_0-ses_b")
	require.NotNil(t, second)
	assert.False(t, second.CompletedAt.IsZero())
	assert.Equal(t, childPath, second.DeclaredTranscriptPath)
}
