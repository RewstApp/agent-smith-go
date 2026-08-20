//go:build darwin

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// detachedCommand launches the auto-update helper detached from the running
// service process. Setsid alone is sufficient on macOS: launchd tears down a
// stopped job by BSD process group (killpg), and setsid() moves the helper
// into a brand new session/process group, so it is already outside the group
// launchd kills. Linux's KillMode=control-group instead kills by cgroup
// membership, which setsid() does not change, so Linux needs a different
// mechanism — see run_command_linux.go.
func detachedCommand(path string, args []string, stdout, stderr *os.File) *exec.Cmd {
	cmd := exec.Command(path, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}
