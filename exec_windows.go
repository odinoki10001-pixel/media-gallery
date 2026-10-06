//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hiddenCmd запускает процесс без окна консоли.
// CREATE_NO_WINDOW = 0x08000000
func hiddenCmd(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
	return cmd
}
