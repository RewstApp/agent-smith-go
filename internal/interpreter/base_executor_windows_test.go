//go:build windows

package interpreter

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/hashicorp/go-hclog"
)

// processRunning shells out to tasklist to check whether pid is still alive.
// The command's own worker pool has already been released by the time this
// runs, so this is a plain, independent check of endpoint state — exactly
// what a customer noticing "unexplained runaway processes" would look at.
func processRunning(t *testing.T, pid int) bool {
	t.Helper()
	out, err := exec.Command("tasklist", "/fi", fmt.Sprintf("PID eq %d", pid)).Output()
	if err != nil {
		t.Fatalf("tasklist failed: %v", err)
	}
	return strings.Contains(string(out), strconv.Itoa(pid))
}

// TestBaseExecutor_CommandTimeout_KillsChildProcess pins the fix for the
// no-job-object leak: a PowerShell command that spawns a child via
// Start-Process and then hangs must, once the per-command timeout fires,
// leave that child dead too — not just the killed shell. Before the job
// object, Process.Kill on Windows only reaped the immediate shell process.
func TestBaseExecutor_CommandTimeout_KillsChildProcess(t *testing.T) {
	executor := NewPowershellExecutor()

	logger := hclog.NewNullLogger()
	// Long enough for Start-Process to actually launch a whole nested
	// powershell.exe and write the pid file before the timeout fires; 1s was
	// flaky in CI because that startup alone can take close to a second.
	timeout := 5
	device := agent.Device{RewstOrgId: "test-org-windows-child", CommandTimeoutSeconds: &timeout}

	pidFile := filepath.Join(t.TempDir(), "child.pid")

	// Spawn a detached long-lived child, record its pid, then hang well past
	// the command timeout so the parent shell is killed while the child is
	// still alive. File.WriteAllText (not Out-File, which Windows PowerShell
	// 5.1 writes as UTF-16LE with a BOM) keeps the pid file plain ASCII so it
	// parses with a simple Atoi below.
	script := fmt.Sprintf(
		"$p = Start-Process -FilePath 'powershell' -ArgumentList '-NoProfile','-Command','Start-Sleep -Seconds 60' -WindowStyle Hidden -PassThru; "+
			"[System.IO.File]::WriteAllText('%s', \"$($p.Id)\"); "+
			"Start-Sleep -Seconds 30",
		pidFile,
	)

	msg := Message{PostId: "test:windows-child", Commands: encodeCommand(script)}

	done := make(chan []byte, 1)
	go func() {
		done <- executor.Execute(context.Background(), &msg, device, logger, nil, nil)
	}()

	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Execute did not return; command was not killed by timeout")
	}

	// The child's pid is written before the hang, so it must be present by
	// the time Execute (killed at ~5s) returns.
	pidBytes, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("failed to read child pid file: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if err != nil {
		t.Fatalf("pid file did not contain a valid pid: %q", pidBytes)
	}

	// Give the job-object teardown a brief moment to complete, then confirm
	// the child no longer survives.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && processRunning(t, pid) {
		time.Sleep(200 * time.Millisecond)
	}
	if processRunning(t, pid) {
		t.Errorf("child process %d survived the parent's timeout kill", pid)
	}
}
