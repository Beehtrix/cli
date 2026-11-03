//go:build !windows

package strategy

import "golang.org/x/sys/unix"

// canExecuteHook reports whether this process may execute the file at path. It
// asks the kernel with access(X_OK), which is what git does before it runs a
// hook and what sh's [ -x ] does, so ownership, the permission class that
// applies, POSIX ACLs and root all count the same way they do for them.
func canExecuteHook(path string) bool {
	return unix.Access(path, unix.X_OK) == nil
}
