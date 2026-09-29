//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// detachConsole gives the child its own hidden console, as a background
// launch does (Start-Process -WindowStyle Hidden, or an agent's shell tool):
// a console exists, but nothing is reading or answering it.
func detachConsole(cmd *exec.Cmd) {
	const createNewConsole = 0x00000010
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewConsole, HideWindow: true}
}
