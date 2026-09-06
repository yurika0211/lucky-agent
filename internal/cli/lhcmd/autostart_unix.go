//go:build !windows

package lhcmd

import (
	"os/exec"
	"syscall"
)

// detachProcAttr configures cmd so it survives the parent process exiting
// and is not affected by the parent's controlling terminal.
func detachProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
