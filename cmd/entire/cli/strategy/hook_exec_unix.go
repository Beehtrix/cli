//go:build !windows

package strategy

import (
	"io/fs"
	"os"
	"slices"
	"syscall"
)

// canExecuteByMode is canExecuteHook from the mode bits alone, for when the
// kernel cannot be asked through the descriptor: the one permission class the
// kernel checks for this process (owner, group or other), as access(X_OK)
// does, and any execute bit for root. It cannot see POSIX ACLs.
func canExecuteByMode(info fs.FileInfo) bool {
	perm := info.Mode().Perm()
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return perm&0o111 != 0
	}
	uid := os.Getuid()
	if uid == 0 {
		return perm&0o111 != 0
	}
	if int(st.Uid) == uid {
		return perm&0o100 != 0
	}
	gid := int(st.Gid)
	if gid == os.Getgid() {
		return perm&0o010 != 0
	}
	if groups, err := os.Getgroups(); err == nil && slices.Contains(groups, gid) {
		return perm&0o010 != 0
	}
	return perm&0o001 != 0
}
