//go:build !windows

package main

import "os/exec"

// detachConsole is a no-op outside Windows: exec already gives the child no
// controlling terminal on stdin/stdout.
func detachConsole(*exec.Cmd) {}
