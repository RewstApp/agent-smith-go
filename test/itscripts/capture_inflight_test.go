// Package itscripts runs the e2e workflow's helper scripts against fabricated
// agent state under a plain `go test ./...`, so a change in what they key on
// fails here rather than twenty minutes into a workflow run.
package itscripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func pwsh(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh not available")
	}
	return p
}

func runCapture(t *testing.T, logFile, command string, baseline string) (int, string, string) {
	t.Helper()
	script, err := filepath.Abs(filepath.Join(
		"..", "..", ".github", "workflows", "it-scripts",
		"capture-the-in-flight-script-file-path.ps1",
	))
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "output")
	cmd := exec.Command(pwsh(t), "-NoProfile", "-File", script)
	cmd.Env = append(os.Environ(),
		"LOG_FILE="+logFile, "BASELINE="+baseline, "COMMAND="+command, "GITHUB_OUTPUT="+out,
	)
	b, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	outputs, _ := os.ReadFile(out)
	return code, string(b), string(outputs)
}

// stepOutputs parses a GITHUB_OUTPUT file into key/value pairs. pwsh writes
// CRLF line endings on Windows, so lines are trimmed rather than matched
// with a literal newline.
func stepOutputs(raw string) map[string]string {
	m := map[string]string{}
	for _, ln := range strings.Split(raw, "\n") {
		ln = strings.TrimRight(ln, "\r")
		if k, v, ok := strings.Cut(ln, "="); ok {
			m[k] = v
		}
	}
	return m
}

// The scenario's long-running command is still on disk; a foreign command
// that arrived later has finished and its script is gone. The capture must
// pick the former, not the newest log line (run 38442590289).
func TestCaptureInFlight_PicksTheStillPresentScriptHoldingTheCommand(t *testing.T) {
	dir := t.TempDir()
	ours := filepath.Join(dir, "exec-111.ps1")
	foreign := filepath.Join(dir, "exec-222.ps1")
	if err := os.WriteFile(ours, []byte("sleep 180\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// foreign is deliberately not written: its command finished and cleaned up.
	logFile := filepath.Join(dir, "agent.log")
	log := strings.Join([]string{
		"[INFO]  agent_smith: Command saved to: message_id=a path=" + ours,
		"[INFO]  agent_smith: Command saved to: message_id=b path=" + foreign,
		"[ERROR] agent_smith: Command failed: error=\"exit status 2\"",
	}, "\n") + "\n"
	if err := os.WriteFile(logFile, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}

	code, out, outputs := runCapture(t, logFile, `"sleep 180"`, "0")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	got := stepOutputs(outputs)
	if got["path"] != ours {
		t.Errorf("captured %q, want our script %s", got["path"], ours)
	}
	if got["scripts_dir"] != dir {
		t.Errorf("scripts_dir = %q, want %s", got["scripts_dir"], dir)
	}
}

// Lines at or below the baseline are a previous scenario's and are ignored
// even when their file still exists and holds the same command.
func TestCaptureInFlight_IgnoresLinesWithinTheBaseline(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "exec-100.ps1")
	ours := filepath.Join(dir, "exec-200.ps1")
	for _, p := range []string{old, ours} {
		if err := os.WriteFile(p, []byte("sleep 180\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	logFile := filepath.Join(dir, "agent.log")
	log := "Command saved to: message_id=a path=" + old + "\nCommand saved to: message_id=b path=" + ours + "\n"
	if err := os.WriteFile(logFile, []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, outputs := runCapture(t, logFile, `"sleep 180"`, "1")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if got := stepOutputs(outputs)["path"]; got != ours {
		t.Errorf("captured %q, want %s", got, ours)
	}
}

func TestCaptureInFlight_RequiresTheCommand(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "agent.log")
	_ = os.WriteFile(logFile, []byte(""), 0o600)
	code, out, _ := runCapture(t, logFile, "", "0")
	if code == 0 || !strings.Contains(out, "COMMAND is required") {
		t.Fatalf("exit %d\n%s", code, out)
	}
}
