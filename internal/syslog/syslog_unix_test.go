//go:build darwin || linux

package syslog

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type mockCommandRunner struct {
	priority string
	source   string
	message  string
	calls    int
	called   bool
	err      error
}

func (m *mockCommandRunner) Run(priority, source, message string) error {
	m.calls++
	m.called = true
	m.priority = priority
	m.source = source
	m.message = message
	return m.err
}

func TestUnixSyslog_Write_InfoPriority(t *testing.T) {
	runner := &mockCommandRunner{}
	s := &unixSyslog{out: &bytes.Buffer{}, source: "test", runner: runner}

	_, err := s.Write([]byte("[INFO] info message"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !runner.called {
		t.Fatal("expected runner to be called")
	}
	if runner.priority != "daemon.info" {
		t.Errorf("expected priority 'daemon.info', got %q", runner.priority)
	}
}

func TestUnixSyslog_Write_WarningPriority(t *testing.T) {
	runner := &mockCommandRunner{}
	s := &unixSyslog{out: &bytes.Buffer{}, source: "test", runner: runner}

	_, err := s.Write([]byte("[WARNING] warning message"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if runner.priority != "daemon.warning" {
		t.Errorf("expected priority 'daemon.warning', got %q", runner.priority)
	}
}

// hclog writes "[WARN] ", not "[WARNING]", so this is the spelling that has to
// map to daemon.warning for real agent warnings.
func TestUnixSyslog_Write_WarnPriority(t *testing.T) {
	runner := &mockCommandRunner{}
	s := &unixSyslog{out: &bytes.Buffer{}, source: "test", runner: runner}

	_, err := s.Write([]byte("2024-01-01T00:00:00.000Z [WARN]  agent_smith: disk space low"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if runner.priority != "daemon.warning" {
		t.Errorf("expected priority 'daemon.warning', got %q", runner.priority)
	}
}

func TestUnixSyslog_Write_ErrorPriority(t *testing.T) {
	runner := &mockCommandRunner{}
	s := &unixSyslog{out: &bytes.Buffer{}, source: "test", runner: runner}

	_, err := s.Write([]byte("[ERROR] error message"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if runner.priority != "daemon.err" {
		t.Errorf("expected priority 'daemon.err', got %q", runner.priority)
	}
}

func TestUnixSyslog_Write_DefaultsToInfo(t *testing.T) {
	runner := &mockCommandRunner{}
	s := &unixSyslog{out: &bytes.Buffer{}, source: "test", runner: runner}

	_, err := s.Write([]byte("[DEBUG] debug message"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if runner.priority != "daemon.info" {
		t.Errorf("expected default priority 'daemon.info', got %q", runner.priority)
	}
}

func TestUnixSyslog_Write_PassesSource(t *testing.T) {
	runner := &mockCommandRunner{}
	s := &unixSyslog{out: &bytes.Buffer{}, source: "my-app", runner: runner}

	_, err := s.Write([]byte("[INFO] hello world"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if runner.source != "my-app" {
		t.Errorf("expected source 'my-app', got %q", runner.source)
	}
}

func TestUnixSyslog_Write_ForwardsToOut(t *testing.T) {
	runner := &mockCommandRunner{}
	var out bytes.Buffer
	s := &unixSyslog{out: &out, source: "test", runner: runner}

	data := []byte("[INFO] forwarded")
	n, err := s.Write(data)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != len(data) {
		t.Errorf("expected %d bytes written, got %d", len(data), n)
	}
	if out.String() != string(data) {
		t.Errorf("expected out %q, got %q", string(data), out.String())
	}
}

// The on-disk log is the copy operators read after an incident, so a syslog
// failure must never cost it a line.
func TestUnixSyslog_Write_WritesToOutWhenRunnerFails(t *testing.T) {
	runner := &mockCommandRunner{err: errors.New("logger exited 1")}
	var out bytes.Buffer
	s := &unixSyslog{out: &out, source: "test", runner: runner}

	data := []byte("[INFO] still logged\n")
	n, err := s.Write(data)
	if err != nil {
		t.Fatalf("expected the syslog failure to be swallowed, got %v", err)
	}
	if n != len(data) {
		t.Errorf("expected %d bytes written, got %d", len(data), n)
	}
	if !strings.Contains(out.String(), string(data)) {
		t.Errorf("expected the log line in out, got %q", out.String())
	}
	if !strings.Contains(out.String(), "syslog forwarding failed") {
		t.Errorf("expected a diagnostic about the syslog failure, got %q", out.String())
	}
	if !strings.Contains(out.String(), "logger exited 1") {
		t.Errorf("expected the diagnostic to name the underlying error, got %q", out.String())
	}
}

func TestUnixSyslog_Write_WritesToOutWhenRunnerTimesOut(t *testing.T) {
	runner := &mockCommandRunner{
		err: errors.New("logger -p daemon.info -t test timed out after 5s: "),
	}
	var out bytes.Buffer
	s := &unixSyslog{out: &out, source: "test", runner: runner}

	data := []byte("[INFO] timed out but logged\n")
	n, err := s.Write(data)
	if err != nil {
		t.Fatalf("expected the syslog timeout to be swallowed, got %v", err)
	}
	if n != len(data) {
		t.Errorf("expected %d bytes written, got %d", len(data), n)
	}
	if !strings.Contains(out.String(), string(data)) {
		t.Errorf("expected the log line in out, got %q", out.String())
	}
	if !strings.Contains(out.String(), "timed out after") {
		t.Errorf("expected the diagnostic to name the timeout, got %q", out.String())
	}
}

// A real file error still has to surface: hclog gets the on-disk write's
// result, not the syslog one.
func TestUnixSyslog_Write_SurfacesOutError(t *testing.T) {
	outErr := errors.New("disk full")
	s := &unixSyslog{out: &failingWriter{err: outErr}, source: "test", runner: &mockCommandRunner{}}

	n, err := s.Write([]byte("[INFO] message"))
	if !errors.Is(err, outErr) {
		t.Fatalf("expected the on-disk write error, got %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 bytes written, got %d", n)
	}
}

// Once forwarding fails, the agent stops paying the subprocess cost on every
// line until the suppression window elapses.
func TestUnixSyslog_Write_SuppressesForwardingAfterFailure(t *testing.T) {
	runner := &mockCommandRunner{err: errors.New("logger exited 1")}
	var out bytes.Buffer
	s := &unixSyslog{
		out:            &out,
		source:         "test",
		runner:         runner,
		suppressWindow: time.Hour,
	}

	for range 5 {
		if _, err := s.Write([]byte("[INFO] message\n")); err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
	}

	if runner.calls != 1 {
		t.Errorf("expected only the first line to shell out, got %d calls", runner.calls)
	}
	if got := strings.Count(out.String(), "syslog forwarding failed"); got != 1 {
		t.Errorf("expected the failure to be reported once, got %d times", got)
	}
	if got := strings.Count(out.String(), "[INFO] message"); got != 5 {
		t.Errorf("expected all 5 lines in the on-disk log, got %d", got)
	}
}

func TestUnixSyslog_Write_ProbesAndReportsRecovery(t *testing.T) {
	runner := &mockCommandRunner{err: errors.New("logger exited 1")}
	var out bytes.Buffer
	s := &unixSyslog{out: &out, source: "test", runner: runner, suppressWindow: time.Nanosecond}

	if _, err := s.Write([]byte("[INFO] first\n")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	runner.err = nil
	if _, err := s.Write([]byte("[INFO] second\n")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if runner.calls != 2 {
		t.Errorf("expected the elapsed window to allow a probe, got %d calls", runner.calls)
	}
	if !strings.Contains(out.String(), "syslog forwarding recovered") {
		t.Errorf("expected a recovery diagnostic, got %q", out.String())
	}

	// Recovery is reported once, not on every subsequent healthy line.
	if _, err := s.Write([]byte("[INFO] third\n")); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if got := strings.Count(out.String(), "syslog forwarding recovered"); got != 1 {
		t.Errorf("expected recovery reported once, got %d times", got)
	}
}

func TestUnixSyslog_Close(t *testing.T) {
	s := &unixSyslog{out: &bytes.Buffer{}, source: "test", runner: &mockCommandRunner{}}

	if err := s.Close(); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestNewWithRunner_Unix(t *testing.T) {
	runner := &mockCommandRunner{}
	var out bytes.Buffer

	syslogger := newWithRunner("test-source", &out, runner)

	if syslogger == nil {
		t.Fatal("expected non-nil Syslog")
	}
}

func TestNew_Unix(t *testing.T) {
	var out bytes.Buffer

	syslogger, err := New("test-source", &out)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if syslogger == nil {
		t.Fatal("expected non-nil Syslog")
	}
}

func TestLoggerCommandRunner_Run_Success(t *testing.T) {
	r := &loggerCommandRunner{binary: "/bin/echo"}

	if err := r.Run("daemon.info", "test", "message"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestLoggerCommandRunner_Run_ReportsFailure(t *testing.T) {
	r := &loggerCommandRunner{binary: filepath.Join(t.TempDir(), "no-such-logger")}

	err := r.Run("daemon.info", "test", "message")
	if err == nil {
		t.Fatal("expected an error for a missing logger binary, got nil")
	}
	if !strings.Contains(err.Error(), "failed") {
		t.Errorf("expected the error to describe the failure, got %q", err.Error())
	}
}

// A hung logger/syslog daemon must not block the goroutine that is logging.
func TestLoggerCommandRunner_Run_TimesOutOnHungLogger(t *testing.T) {
	r := &loggerCommandRunner{binary: hangingLoggerFixture(t), timeout: 200 * time.Millisecond}

	start := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- r.Run("daemon.info", "test", "message")
	}()

	var err error
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return; the hung logger was not killed by the timeout")
	}

	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("Run took %v to be killed; expected roughly the 200ms timeout", elapsed)
	}
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected the error to mention the timeout, got %q", err.Error())
	}
}

// Write itself must return promptly against a hung logger, with the line on
// disk, because that is what the logging goroutine is waiting on.
func TestUnixSyslog_Write_DoesNotBlockOnHungLogger(t *testing.T) {
	var out bytes.Buffer
	s := &unixSyslog{
		out:    &out,
		source: "test",
		runner: &loggerCommandRunner{
			binary:  hangingLoggerFixture(t),
			timeout: 200 * time.Millisecond,
		},
	}

	data := []byte("[INFO] logged despite the hang\n")
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := s.Write(data); err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Write blocked on the hung logger")
	}

	if !strings.Contains(out.String(), string(data)) {
		t.Errorf("expected the log line in out, got %q", out.String())
	}
}

// hangingLoggerFixture returns the path to a stand-in `logger` that never exits
// on its own, so the timeout is what ends the call.
func hangingLoggerFixture(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "hanging-logger")
	script := "#!/bin/sh\nsleep 30\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("failed to write the hanging logger fixture: %v", err)
	}

	return path
}

type failingWriter struct {
	err error
}

func (w *failingWriter) Write(p []byte) (int, error) {
	return 0, w.err
}
