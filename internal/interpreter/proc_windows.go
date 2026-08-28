//go:build windows

package interpreter

import (
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// configureProcessGroup creates a Windows job object with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE and wires cmd.Cancel to close it.
//
// Without this, cancelling the command's context (per-command timeout or
// connection teardown) falls back to the default exec.CommandContext
// behavior of killing only the shell process itself. Anything the shell
// spawned (Start-Process, an installer, a stuck helper) is reparented and
// keeps running on the endpoint after the worker is released — there is no
// process-group equivalent to kill on Windows. A job object with
// KILL_ON_JOB_CLOSE fixes that: every process assigned to it, direct or
// descendant, is terminated the moment its last handle is closed, which is
// exactly what cmd.Cancel now does. See proc_unix.go for the process-group
// kill this mirrors.
//
// The returned processTree's Assign must run once cmd.Process is set (i.e.
// after Start) to put the shell process into the job; Release must run once
// the command has finished, unconditionally, to close the job handle even on
// the non-cancelled path (where cmd.Cancel never fires).
//
// If job object creation or configuration fails, both hooks become no-ops
// and cmd.Cancel falls back to plain Process.Kill — a command still runs,
// just without the descendant-tree guarantee.
func configureProcessGroup(cmd *exec.Cmd) processTree {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return processTree{}
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return processTree{}
	}

	// Cancel (the non-cancelled completion path) and Release both close the
	// job; closeJob collapses them to a single real close so the second call
	// isn't logged as a spurious "invalid handle" failure.
	var closeOnce sync.Once
	var closeErr error
	closeJob := func() error {
		closeOnce.Do(func() {
			closeErr = windows.CloseHandle(job)
		})
		return closeErr
	}

	cmd.Cancel = func() error {
		// Closing the job's last handle terminates every process still
		// assigned to it (KILL_ON_JOB_CLOSE) — the shell and its full
		// descendant tree, once Assign below has run. Process.Kill also
		// covers the narrow window between Start and Assign.
		_ = closeJob()
		if cmd.Process == nil {
			return nil
		}
		return cmd.Process.Kill()
	}

	return processTree{
		assign: func() error {
			if cmd.Process == nil {
				return nil
			}
			// os.Process only exposes the pid, not the handle os/exec created
			// internally (that requires WithHandle, Go 1.26+; this repo builds
			// on 1.25). Re-opening by pid immediately after Start is the
			// standard fallback; the window in which that pid could have
			// already exited and been reused by an unrelated process is
			// negligible at this point in the call sequence.
			handle, err := windows.OpenProcess(
				windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
				false,
				uint32(cmd.Process.Pid),
			)
			if err != nil {
				return err
			}
			defer windows.CloseHandle(handle)
			return windows.AssignProcessToJobObject(job, handle)
		},
		release: closeJob,
	}
}
