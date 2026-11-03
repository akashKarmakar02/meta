//go:build linux

package cli

import (
	"os/exec"
	"syscall"
)

// setParentDeathSignal asks the kernel to deliver sig to this process when
// its parent exits — including when the parent is killed by SIGKILL and
// cannot run cleanup handlers (A06 §10: the public proxy must not keep
// running after the development session exits). Applied to every child of
// meta dev; other platforms fall back to the session's signal handling.
func setParentDeathSignal(cmd *exec.Cmd, sig syscall.Signal) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: sig}
}
