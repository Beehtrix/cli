package strategy

import (
	"path/filepath"

	"github.com/entireio/cli/cmd/entire/cli/paths"
	"github.com/entireio/cli/cmd/entire/cli/session"
)

// enterWorktreeView makes state's home fields describe its edits in the
// worktree at root instead of its home, so commit-time code written for the
// home — content checks, claims, condensation, carry-forward — works on that
// worktree's record unchanged: FilesTouched, TouchedFileHashes and BaseCommit
// come from its OtherWorktrees entry, WorktreePath and WorktreeID name it, and
// the home's record waits in OtherWorktrees under the home's key, so "pending
// anywhere" (HasPendingFiles) still sees it.
//
// ok is false, and nothing changes, when root is the session's home, the
// session has no home recorded, or no entry for root. Otherwise leave puts
// everything back, keeping what processing left in the view as root's entry
// (dropped once empty). Call leave before the state is saved.
func enterWorktreeView(state *SessionState, root string) (leave func(), ok bool) {
	if root == "" || state.WorktreePath == "" || len(state.OtherWorktrees) == 0 ||
		isSessionHomeWorktree(root, state) || sameDir(root, state.WorktreePath) {
		return nil, false
	}
	worktreeID, err := paths.GetWorktreeID(root)
	if err != nil {
		return nil, false
	}
	key, homeKey := session.WorktreeKey(worktreeID), session.WorktreeKey(state.WorktreeID)
	entry := state.OtherWorktrees[key]
	if entry == nil || key == homeKey {
		return nil, false
	}
	if _, clash := state.OtherWorktrees[homeKey]; clash {
		return nil, false
	}
	home := session.WorktreeWork{
		Path:              state.WorktreePath,
		BaseCommit:        state.BaseCommit,
		FilesTouched:      state.FilesTouched,
		TouchedFileHashes: state.TouchedFileHashes,
	}
	homeID := state.WorktreeID
	delete(state.OtherWorktrees, key)
	state.OtherWorktrees[homeKey] = &home
	state.WorktreePath, state.WorktreeID = root, worktreeID
	state.BaseCommit = entry.BaseCommit
	state.FilesTouched, state.TouchedFileHashes = entry.FilesTouched, entry.TouchedFileHashes
	state.InWorktreeView = true

	return func() {
		left := session.WorktreeWork{
			Path:              entry.Path,
			BaseCommit:        state.BaseCommit,
			FilesTouched:      state.FilesTouched,
			TouchedFileHashes: state.TouchedFileHashes,
		}
		if left.Path == "" {
			left.Path = root
		}
		home := state.OtherWorktrees[homeKey]
		delete(state.OtherWorktrees, homeKey)
		state.WorktreePath, state.WorktreeID = home.Path, homeID
		state.BaseCommit = home.BaseCommit
		state.FilesTouched, state.TouchedFileHashes = home.FilesTouched, home.TouchedFileHashes
		state.InWorktreeView = false
		if len(left.FilesTouched) > 0 {
			state.OtherWorktrees[key] = &left
		}
		if len(state.OtherWorktrees) == 0 {
			state.OtherWorktrees = nil
		}
	}, true
}

// hasWorktreeEntry reports whether state recorded edits in the worktree at
// root other than its home.
func hasWorktreeEntry(state *SessionState, root string) bool {
	if len(state.OtherWorktrees) == 0 || state.WorktreePath == "" || isSessionHomeWorktree(root, state) || sameDir(root, state.WorktreePath) {
		return false
	}
	worktreeID, err := paths.GetWorktreeID(root)
	if err != nil {
		return false
	}
	work := state.OtherWorktrees[session.WorktreeKey(worktreeID)]
	return work != nil && len(work.FilesTouched) > 0
}

// overlapsFiles reports whether any of files is in set.
func overlapsFiles(files []string, set map[string]struct{}) bool {
	for _, f := range files {
		if _, ok := set[filepath.ToSlash(f)]; ok {
			return true
		}
	}
	return false
}
