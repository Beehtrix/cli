//go:build !windows

package main

import "os/exec"

// startLikeDesktopAgentShell needs nothing here: the agent's own process
// already decides the terminal git inherits. See gitproc_windows.go.
func startLikeDesktopAgentShell(_ *exec.Cmd) {}
