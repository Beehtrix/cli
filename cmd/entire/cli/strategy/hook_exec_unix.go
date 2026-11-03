//go:build !windows

package strategy

import (
	"io/fs"
	"os"
	"slices"
	"syscall"
)

// canExecuteHook reports whether this process may execute a file with info,
// using the one permission class the kernel checks (owner, group or other)
// the way git's and sh's access(X_OK) does. A file with only group or other
// execute bits is not runnable by its owner, so git skips it as a hook and
// Brain's [ -x ] guard skips it as a chained copy.
func canExecuteHook(info fs.FileInfo) bool {
	perm := info.Mode().Perm()
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return perm&0o111 != 0
	}
	euid := os.Geteuid()
	if euid == 0 {
		return perm&0o111 != 0
	}
	if int(st.Uid) == euid {
		return perm&0o100 != 0
	}
	gid := int(st.Gid)
	if gid == os.Getegid() {
		return perm&0o010 != 0
	}
	if groups, err := os.Getgroups(); err == nil && slices.Contains(groups, gid) {
		return perm&0o010 != 0
	}
	return perm&0o001 != 0
}
