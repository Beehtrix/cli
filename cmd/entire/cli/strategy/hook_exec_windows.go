//go:build windows

package strategy

// canExecuteHook: Windows has no execute bit, so any regular file can run.
func canExecuteHook(string) bool { return true }
