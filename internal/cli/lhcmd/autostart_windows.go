//go:build windows

package lhcmd

import (
	"os/exec"
	"syscall"
)

// detachProcAttr configures cmd so it doesn't open a visible console
// window and isn't tied to the parent's console.
func detachProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | 0x08000000, // CREATE_NO_WINDOW
	}
}
