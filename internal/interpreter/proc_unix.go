//go:build darwin || linux

package interpreter

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup places the command in its own process group and wires a
// Cancel hook that kills the entire group when the command's context is
// cancelled (per-command timeout or connection teardown).
//
// Without this, cancelling the context only signals the shell process itself.
// Any child the shell spawned (a `sleep`, a stuck network client, a blocked
// read) is reparented and keeps running, and — because it inherits the shell's
// stdout/stderr pipe — keeps cmd.Wait blocked. That would leave the worker
// wedged exactly as the timeout is meant to prevent. Killing the whole group
// tears down the descendant tree so the pipe closes and Wait returns.
func configureProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// A negative pid targets the entire process group led by the shell.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
