//go:build windows

package interpreter

import "os/exec"

// configureProcessGroup is a no-op on Windows. Context cancellation falls back to
// the default exec.CommandContext behavior (Process.Kill) plus the WaitDelay
// backstop set by the caller, which is sufficient to release the worker.
// Terminating a full descendant tree on Windows requires job objects and is out
// of scope for this change.
func configureProcessGroup(cmd *exec.Cmd) {}
