//go:build !windows

package main

import "os/exec"

// hiddenCmd на не-Windows платформах — обычный exec.Command.
func hiddenCmd(name string, args ...string) *exec.Cmd {
	return exec.Command(name, args...)
}
