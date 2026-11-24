package hermeticscripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// verifyAutoUpdate runs .github/actions/verify-auto-update/verify.ps1 the way
// the action does, with its parameters, and returns its exit code, output and
// how long it took.
func verifyAutoUpdate(t *testing.T, logFile string, waitSeconds int) (int, string, time.Duration) {
	t.Helper()
	script, err := filepath.Abs(
		filepath.Join("..", "..", ".github", "actions", "verify-auto-update", "verify.ps1"),
	)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(pwshPath(t), "-NoProfile", "-File", script,
		"-LogFile", logFile, "-WaitSeconds", strconv.Itoa(waitSeconds))
	start := time.Now()
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running verify.ps1: %v\n%s", err, out)
		}
		code = exitErr.ExitCode()
	}
	return code, string(out), time.Since(start)
}

const (
	releaseStart = "2026-11-24T00:38:41.598Z [INFO]  agent_smith: Agent Smith started: version=v1.10.0 os=windows\n"
	itStart      = "2026-11-24T00:38:45.846Z [INFO]  agent_smith: Agent Smith started: version=v0.0.0-it os=windows\n"
	itDecided    = "2026-11-24T00:39:15.846Z [INFO]  agent_smith: Checking for updates: channel=stable\n" +
		"2026-11-24T00:39:16.144Z [INFO]  agent_smith: Updating agent: version=v1.10.0\n" +
		"2026-11-24T00:39:16.619Z [INFO]  agent_smith: Deferring update until in-flight commands finish: executing=1\n"
	updatedStart = "2026-11-24T00:39:50.000Z [INFO]  agent_smith: Agent Smith started: version=v1.10.0 os=windows\n"
)

// The update lands after the restart was held for a running command (run
// 45298389070): the check waits for it instead of reading the log once after
// a fixed sleep, and passes as soon as the new version has started.
func TestVerifyAutoUpdate_WaitsForAHeldRestart(t *testing.T) {
	log := filepath.Join(t.TempDir(), "rewst_agent.log")
	if err := os.WriteFile(log, []byte(releaseStart+itStart+itDecided), 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(3 * time.Second) // sleep-ok: the held restart the script must wait out
		f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return
		}
		_, _ = f.WriteString(updatedStart)
		_ = f.Close()
	}()

	code, out, took := verifyAutoUpdate(t, log, 60)
	if code != 0 || !strings.Contains(out, "Auto updater integration test passed") {
		t.Fatalf("a held but completed update failed: exit %d\n%s", code, out)
	}
	if took > 30*time.Second {
		t.Errorf("took %s; it must pass as soon as the new version starts, not at the bound", took)
	}
}

// An update that never lands still fails, once the bound has passed, naming
// the integration version still running.
func TestVerifyAutoUpdate_FailsWhenTheUpdateNeverLands(t *testing.T) {
	log := filepath.Join(t.TempDir(), "rewst_agent.log")
	if err := os.WriteFile(log, []byte(releaseStart+itStart+itDecided), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, took := verifyAutoUpdate(t, log, 4)
	if code == 0 || !strings.Contains(out, "still running the integration test version 4 s after") {
		t.Fatalf("an update that never landed passed: exit %d\n%s", code, out)
	}
	if took < 4*time.Second {
		t.Errorf("gave up after %s, before its 4 s bound", took)
	}
}

// A log without the updater's lines fails whatever version is running.
func TestVerifyAutoUpdate_FailsWithoutTheUpdaterLines(t *testing.T) {
	log := filepath.Join(t.TempDir(), "rewst_agent.log")
	if err := os.WriteFile(log, []byte(itStart+updatedStart), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ := verifyAutoUpdate(t, log, 4)
	const want = "Expected auto update log line not found: Updating agent"
	if code == 0 || !strings.Contains(out, want) {
		t.Fatalf("a log without the updater's lines passed: exit %d\n%s", code, out)
	}
}
