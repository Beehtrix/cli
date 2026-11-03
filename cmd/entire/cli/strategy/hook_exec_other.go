//go:build !linux && !windows

package strategy

import (
	"io/fs"
	"os"
)

// canExecuteHook reports whether this process may execute f, the hook file
// opened through its root, with info its Stat. Outside Linux the descriptor
// cannot be asked, so the mode bits decide (canExecuteByMode).
func canExecuteHook(_ *os.File, info fs.FileInfo) bool {
	return canExecuteByMode(info)
}
