//go:build windows

package strategy

import "io/fs"

// canExecuteHook: Windows has no execute bit, so any regular file can run.
func canExecuteHook(fs.FileInfo) bool { return true }
