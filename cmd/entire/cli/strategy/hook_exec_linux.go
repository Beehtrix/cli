//go:build linux

package strategy

import (
	"errors"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

// canExecuteHook reports whether this process may execute f, the hook file
// opened through its root, with info its Stat. It asks the kernel about the
// open descriptor (faccessat2 with AT_EMPTY_PATH and the real ids, which is
// git's access(X_OK) before it runs a hook and sh's [ -x ]), so ownership, the
// permission class that applies, POSIX ACLs and root count as they do for
// them, and no path is resolved outside the root. Kernels before 5.8 have no
// faccessat2; there the mode bits decide (canExecuteByMode).
func canExecuteHook(f *os.File, info fs.FileInfo) bool {
	err := unix.Faccessat(int(f.Fd()), "", unix.X_OK, unix.AT_EMPTY_PATH)
	switch {
	case err == nil:
		return true
	case errors.Is(err, unix.EACCES):
		return false
	default:
		return canExecuteByMode(info)
	}
}
