//go:build !linux

package cli

import (
	"os"
	"os/exec"
	"syscall"
)

// setParentDeathSignal is a no-op where the platform has no parent-death
// signal: shutdown relies on meta dev's own signal handling.
func setParentDeathSignal(*exec.Cmd, syscall.Signal) {}

func signalChild(cmd *exec.Cmd, sig os.Signal) error { return cmd.Process.Signal(sig) }
