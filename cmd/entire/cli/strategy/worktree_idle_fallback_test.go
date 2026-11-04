package strategy

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/entireio/cli/cmd/entire/cli/session"
)

// Uses t.Chdir — do NOT add t.Parallel().

// A commit from a worktree with no session of its own falls back to the
// sessions of a sibling worktree. One whose last interaction is older than
// recentSessionWindow is not plausibly the commit's session, so it is not
// linked: post-commit would otherwise condense its whole transcript into every
// commit made here (cli issue 2979). A session with no recorded interaction is
// kept, as before.
func TestCommitLinking_FallbackSkipsSiblingSessionsIdleBeyondTheWindow(t *testing.T) {
	now := time.Now()
	dayAgo := now.Add(-24 * time.Hour)
	justPast := now.Add(-recentSessionWindow - time.Minute)
	recent := now.Add(-time.Minute)
	for _, tt := range []struct {
		name     string
		sessions map[string]*time.Time
		want     []string
	}{
		{name: "idle for a day", sessions: map[string]*time.Time{"stale": &dayAgo}},
		{name: "idle just past the window", sessions: map[string]*time.Time{"stale": &justPast}},
		{name: "recent", sessions: map[string]*time.Time{"recent": &recent}, want: []string{"recent"}},
		{name: "no recorded interaction", sessions: map[string]*time.Time{"undated": nil}, want: []string{"undated"}},
		{
			name:     "stale beside recent and undated",
			sessions: map[string]*time.Time{"stale": &dayAgo, "recent": &recent, "undated": nil},
			want:     []string{"recent", "undated"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mainDir, linkedDir := twoWorktrees(t)
			ctx := context.Background()
			s := &ManualCommitStrategy{}
			for id, last := range tt.sessions {
				saveSessionMatchState(ctx, t, s, mainDir, &SessionState{SessionID: id, WorktreePath: mainDir, LastInteractionTime: last})
			}
			assert.ElementsMatch(t, tt.want, linkingIDs(t, linkedDir))
		})
	}
}

// An idle sibling session that recorded edits in the committing worktree still
// joins the commit, held to those edits: the idle check only narrows the
// fallback, not the sessions that edited this worktree from elsewhere.
func TestCommitLinking_IdleSiblingSessionThatEditedHereStillJoins(t *testing.T) {
	mainDir, linkedDir := twoWorktrees(t)
	ctx := context.Background()
	s := &ManualCommitStrategy{}
	dayAgo := time.Now().Add(-24 * time.Hour)
	saveSessionMatchState(ctx, t, s, mainDir, &SessionState{
		SessionID:           "edited-here",
		WorktreePath:        mainDir,
		LastInteractionTime: &dayAgo,
		OtherWorktrees: map[string]*session.WorktreeWork{
			linkedKey(t, linkedDir): {Path: linkedDir, FilesTouched: []string{"feature.go"}, Incarnation: mark(t, linkedDir)},
		},
	})

	linked := linkingSessions(t, linkedDir)
	require.Contains(t, linked, "edited-here")
	assert.True(t, linked["edited-here"].LinkedByWorktreeEntry, "held to the edits it recorded here")
}
