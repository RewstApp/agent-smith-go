package utils

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// helperSleepEnv marks the re-executed test binary as the sleeping child rather
// than a normal test run.
const helperSleepEnv = "AGENT_SMITH_TEST_HELPER_SLEEP"

// TestHelperProcessSleeps is not a test: it is the body of the child process
// spawned by TestProcessRunningFromExecutable, kept alive long enough to be
// observed and then killed.
func TestHelperProcessSleeps(t *testing.T) {
	if os.Getenv(helperSleepEnv) != "1" {
		t.Skip("helper process; only runs when re-executed by the process scan test")
	}
	time.Sleep(30 * time.Second)
}

// The scan has to answer both halves of the question it is used for: it must see
// a live process running the binary, and it must stop seeing it once that
// process is gone. A check that only ever says "running" would wedge every
// update; one that only ever says "gone" would be the fixed sleep again.
func TestProcessRunningFromExecutable(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("failed to resolve the test binary: %v", err)
	}

	child := exec.Command(self, "-test.run=TestHelperProcessSleeps")
	child.Env = append(os.Environ(), helperSleepEnv+"=1")
	if err := child.Start(); err != nil {
		t.Fatalf("failed to start the helper process: %v", err)
	}
	killed := false
	defer func() {
		if !killed {
			_ = child.Process.Kill()
			_, _ = child.Process.Wait()
		}
	}()

	waitFor(t, "the helper process to be detected", func() bool {
		running, err := ProcessRunningFromExecutable(self)
		if err != nil {
			t.Fatalf("failed to scan processes: %v", err)
		}
		return running
	})

	if err := child.Process.Kill(); err != nil {
		t.Fatalf("failed to kill the helper process: %v", err)
	}
	_, _ = child.Process.Wait()
	killed = true

	waitFor(t, "the helper process to disappear", func() bool {
		running, err := ProcessRunningFromExecutable(self)
		if err != nil {
			t.Fatalf("failed to scan processes: %v", err)
		}
		return !running
	})
}

// The caller's own process must never count as the old agent, so an operator who
// runs the installed binary in place is not mistaken for the service.
func TestProcessRunningFromExecutable_ExcludesSelf(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatalf("failed to resolve the test binary: %v", err)
	}

	running, err := ProcessRunningFromExecutable(self)
	if err != nil {
		t.Fatalf("failed to scan processes: %v", err)
	}
	if running {
		t.Error("expected the calling process to be excluded from the scan")
	}
}

func TestProcessRunningFromExecutable_UnknownPath(t *testing.T) {
	running, err := ProcessRunningFromExecutable(filepath.Join(t.TempDir(), "never-installed"))
	if err != nil {
		t.Fatalf("failed to scan processes: %v", err)
	}
	if running {
		t.Error("expected no process to be running a binary that does not exist")
	}
}

// waitFor polls condition until it holds or the test gives up, so the assertions
// do not depend on how quickly the operating system reflects a process change.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()

	deadline := time.Now().Add(15 * time.Second)
	for {
		if condition() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
