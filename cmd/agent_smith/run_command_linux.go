//go:build linux

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// detachedCommand launches the auto-update helper (path, args) inside its own
// transient systemd scope instead of running it as a direct child of the
// service process. The helper's job is to call `systemctl stop` on the very
// unit it was launched from; a plain child inherits that unit's cgroup, and
// systemd's default KillMode=control-group kills every process in the
// cgroup — including the helper itself — the moment the stop is issued, before
// it can replace the binary or start the service again. `systemd-run --scope`
// creates a new transient scope (and cgroup) for the helper up front, so it is
// never a member of the unit's cgroup and survives stopping it. `--collect`
// releases the transient scope unit's bookkeeping once the helper exits so
// scopes don't accumulate one per update.
func detachedCommand(path string, args []string, stdout, stderr *os.File) *exec.Cmd {
	scopeArgs := append([]string{"--scope", "--collect", "--quiet", "--", path}, args...)
	cmd := exec.Command("systemd-run", scopeArgs...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return cmd
}
