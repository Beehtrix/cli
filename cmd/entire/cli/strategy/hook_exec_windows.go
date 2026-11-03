//go:build windows

package strategy

import (
	"io/fs"
	"os"
)

// canExecuteHook: Windows has no execute bit, so any regular file can run.
func canExecuteHook(*os.File, fs.FileInfo) bool { return true }
