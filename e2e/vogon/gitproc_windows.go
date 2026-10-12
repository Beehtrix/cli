//go:build windows

package main

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// startLikeDesktopAgentShell gives git a console with no window, the console a
// desktop agent (Claude Code, Codex) gives the shell it runs commands in. The
// commit's hooks then face what they face on a user's machine: a console that
// opens like any other but that nobody can see (#2725, #2726).
func startLikeDesktopAgentShell(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}
