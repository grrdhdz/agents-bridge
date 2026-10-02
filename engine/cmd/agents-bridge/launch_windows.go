//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// detachedProcess is DETACHED_PROCESS: no console attached to the child.
const detachedProcess = 0x00000008

// detachAttrs starts the child in its own process group with no console, so
// the console's Ctrl+C never reaches it and it outlives the TUI.
func detachAttrs(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | detachedProcess, HideWindow: true}
}
