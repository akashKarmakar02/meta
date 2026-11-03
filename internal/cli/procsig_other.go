//go:build !linux

package cli

import (
	"os/exec"
	"syscall"
)

// setParentDeathSignal is a no-op where the platform has no parent-death
// signal: shutdown relies on meta dev's own signal handling.
func setParentDeathSignal(*exec.Cmd, syscall.Signal) {}
