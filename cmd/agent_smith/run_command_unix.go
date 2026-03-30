//go:build darwin || linux

package main

import (
	"os"
	"os/exec"
	"syscall"
)

func detachedCommand(path string, args []string, stdout, stderr *os.File) *exec.Cmd {
	cmd := exec.Command(path, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}
