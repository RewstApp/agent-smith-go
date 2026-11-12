//go:build windows

package interpreter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/hashicorp/go-hclog"
	"golang.org/x/sys/windows"
)

// Win32 values x/sys/windows does not name.
const (
	// stillActive is the exit code GetExitCodeProcess reports for a process
	// that has not terminated.
	stillActive = 259
	// waitTimeout is WaitForSingleObject's return when the timeout elapsed
	// before the handle was signalled.
	waitTimeout = 0x00000102
)

// childProcess is a handle to the spawned grandchild, held open for the
// whole test. A process handle pins the kernel object: the pid cannot be
// reused by an unrelated process while it is open, GetExitCodeProcess answers
// from the object rather than from the process table, and the handle itself
// is signalled the moment the process terminates - which is what
// WaitForSingleObject below waits on.
//
// The previous version of this test shelled out to tasklist and looked for the
// pid's digits anywhere in its output. That depended on how quickly the
// process table reflected the termination (hence a polling loop and a 5s
// budget around it) and could be fooled either way: a pid reused by another
// process read as "survived", and the digits matching a column other than
// PID (memory usage, session number) read as "running" for a process that
// was not. Neither can happen through a handle.
type childProcess struct {
	handle windows.Handle
}

// openChild opens the grandchild by pid. OpenProcess succeeding is the
// observability check: the process exists at this moment and will stay
// addressable for the rest of the test.
func openChild(t *testing.T, pid int) *childProcess {
	t.Helper()
	handle, err := windows.OpenProcess(
		windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION,
		false,
		uint32(pid),
	)
	if err != nil {
		t.Fatalf("OpenProcess(%d) failed; the child is not observable: %v", pid, err)
	}
	c := &childProcess{handle: handle}
	t.Cleanup(func() { _ = windows.CloseHandle(handle) })
	return c
}

// running reports whether the process has not yet terminated.
func (c *childProcess) running(t *testing.T) bool {
	t.Helper()
	var code uint32
	if err := windows.GetExitCodeProcess(c.handle, &code); err != nil {
		t.Fatalf("GetExitCodeProcess failed: %v", err)
	}
	return code == stillActive
}

// waitExit blocks until the process terminates or timeout elapses and reports
// whether it terminated. There is no polling and no sleep: the kernel
// signals the handle on exit.
func (c *childProcess) waitExit(t *testing.T, timeout time.Duration) bool {
	t.Helper()
	event, err := windows.WaitForSingleObject(c.handle, uint32(timeout/time.Millisecond))
	switch {
	case err != nil:
		t.Fatalf("WaitForSingleObject failed: %v", err)
	case event == windows.WAIT_OBJECT_0:
		return true
	case event == waitTimeout:
		return false
	}
	t.Fatalf("WaitForSingleObject returned unexpected event %#x", event)
	return false
}

// waitForChildPid polls pidFile until the spawned child has recorded its pid,
// then returns it.
//
// The wait is deliberately on the test's own deadline rather than the command's
// timeout budget. The previous version of this test gave the command a 5s
// timeout and then assumed the pid file existed by the time Execute returned,
// which made the assertion a race against how long Start-Process takes to
// launch a whole nested powershell.exe — close to a second on a healthy runner
// and longer on a loaded one. When that overran, the shell was killed before it
// could write the file and the test failed with "failed to read child pid
// file", reporting a CI timing artifact as a job-object regression. Raising the
// timeout (1s -> 5s once already) only narrows that window; polling on an
// independent deadline closes it, because nothing tears the command down until
// the test itself asks for it.
func waitForChildPid(t *testing.T, pidFile string) int {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)
	for {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil {
				return pid
			}
			// The file exists but the write is still in flight; keep polling.
		} else if !os.IsNotExist(err) {
			t.Fatalf("failed to read child pid file: %v", err)
		}

		if time.Now().After(deadline) {
			t.Fatalf("child pid file %s was never written; the child never started", pidFile)
		}
		time.Sleep(100 * time.Millisecond) // sleep-ok: poll interval
	}
}

// TestBaseExecutor_ContextCancel_KillsChildProcess pins the fix for the
// no-job-object leak: a PowerShell command that spawns a child via
// Start-Process and then hangs must, once the command's context is torn down,
// leave that child dead too — not just the killed shell. Before the job
// object, Process.Kill on Windows only reaped the immediate shell process.
//
// The teardown is triggered by cancelling the parent context rather than by
// letting the per-command timeout expire. Both funnel through exactly the same
// mechanism — Execute derives execCtx from the caller's ctx, so either source
// cancels it, which fires cmd.Cancel and closes the job object — but only
// cancellation is under the test's control. The command timeout is set far
// beyond anything this test waits for, so the shell is guaranteed the time it
// needs to launch the child and record its pid before anything kills it.
// TestBaseExecutor_CommandTimeout_KillsHungScript covers the other half: that
// an expiring timeout is what cancels execCtx in the first place.
func TestBaseExecutor_ContextCancel_KillsChildProcess(t *testing.T) {
	executor := NewPowershellExecutor()

	logger := hclog.NewNullLogger()
	// Far longer than any deadline this test waits on, so the timeout can never
	// be the thing that ends the command. Cancellation below is the trigger.
	timeout := 600
	device := agent.Device{RewstOrgId: "test-org-windows-child", CommandTimeoutSeconds: &timeout}

	pidFile := filepath.Join(t.TempDir(), "child.pid")

	// Spawn a detached long-lived child, record its pid, then hang. The hang
	// outlasts every deadline below so the shell is still alive, with the child
	// still alive under it, at the moment the context is cancelled.
	// File.WriteAllText (not Out-File, which Windows PowerShell 5.1 writes as
	// UTF-16LE with a BOM) keeps the pid file plain ASCII so it parses with a
	// simple Atoi.
	script := fmt.Sprintf(
		"$p = Start-Process -FilePath 'powershell' -ArgumentList '-NoProfile','-Command','Start-Sleep -Seconds 120' -WindowStyle Hidden -PassThru; "+
			"[System.IO.File]::WriteAllText('%s', \"$($p.Id)\"); "+
			"Start-Sleep -Seconds 120",
		pidFile,
	)

	msg := Message{PostId: "test:windows-child", Commands: encodeCommand(script)}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan []byte, 1)
	go func() {
		done <- executor.Execute(ctx, &msg, device, logger, nil, nil)
	}()

	// Wait for the child to exist on the test's own deadline, open a handle to
	// it, and confirm it is genuinely running. Asserting it is alive *before*
	// the kill is what stops a child that never started, or that exited on its
	// own, from being mistaken for a successful teardown below; holding the
	// handle is what makes the after-kill check answer about this process and
	// no other.
	pid := waitForChildPid(t, pidFile)
	child := openChild(t, pid)
	if !child.running(t) {
		t.Fatalf("child process %d was not running before cancellation", pid)
	}

	cancel()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Execute did not return; command was not killed by cancellation")
	}

	// The job object's last handle is closed before Execute returns, and
	// KILL_ON_JOB_CLOSE terminates every process in it at that moment; the
	// wait here only covers the kernel finishing that termination. It is
	// generous relative to that, and it ends as soon as the handle is
	// signalled, so it is not a budget the test spends.
	if !child.waitExit(t, 30*time.Second) {
		t.Errorf("child process %d survived the parent's cancellation kill", pid)
	}
}

// TestBaseExecutor_CommandTimeout_KillsHungScript pins the half of the teardown
// that TestBaseExecutor_ContextCancel_KillsChildProcess deliberately does not:
// that an expiring per-command timeout is what cancels execCtx, frees the
// worker, and reports the result as timed out. It mirrors the Unix test of the
// same name.
//
// It spawns no child, so unlike the old combined test there is nothing here
// racing the timeout budget — the only thing that has to happen within the
// deadline is the shell's own Start-Sleep, which is already running.
func TestBaseExecutor_CommandTimeout_KillsHungScript(t *testing.T) {
	executor := NewPowershellExecutor()

	var buf bytes.Buffer
	logger := hclog.New(&hclog.LoggerOptions{Output: &buf, Level: hclog.Error})
	timeout := 5
	device := agent.Device{RewstOrgId: "test-org-windows-timeout", CommandTimeoutSeconds: &timeout}

	// A script that would otherwise block a worker indefinitely.
	msg := Message{
		PostId:   "test:windows-timeout",
		Commands: encodeCommand("Start-Sleep -Seconds 120"),
	}

	start := time.Now()
	done := make(chan []byte, 1)
	go func() {
		done <- executor.Execute(context.Background(), &msg, device, logger, nil, nil)
	}()

	var resultJSON []byte
	select {
	case resultJSON = <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("Execute did not return; command was not killed by timeout")
	}

	// Generous relative to the 5s timeout: this asserts the timeout is what
	// ended the command rather than the 120s sleep running to completion, not
	// that the runner is fast.
	if elapsed := time.Since(start); elapsed > 45*time.Second {
		t.Errorf("command took %v to be killed; expected roughly the 5s timeout", elapsed)
	}

	var r result
	if err := json.Unmarshal(resultJSON, &r); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if !r.TimedOut {
		t.Errorf("expected timed_out=true, got result %s", resultJSON)
	}
	if !strings.Contains(r.Error, "timed out") {
		t.Errorf("expected error to mention timeout, got %q", r.Error)
	}

	// The timeout must be logged at Error level with the post_id for diagnosis.
	logs := buf.String()
	if !strings.Contains(logs, "Command timed out") {
		t.Errorf("expected an Error-level timeout log, got %q", logs)
	}
	if !strings.Contains(logs, "test:windows-timeout") {
		t.Errorf("expected the timeout log to include the post_id, got %q", logs)
	}
}
