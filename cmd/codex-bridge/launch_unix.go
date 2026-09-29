//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

// detachAttrs puts the child in its own session: no controlling terminal,
// so the terminal's Ctrl+C / hangup never reaches it and it outlives the TUI.
func detachAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
