//go:build linux

package syslog

import (
	"bytes"
	"testing"
)

type mockCommandRunner struct {
	priority string
	source   string
	message  string
	called   bool
}

func (m *mockCommandRunner) Run(priority, source, message string) error {
	m.called = true
	m.priority = priority
	m.source = source
	m.message = message
	return nil
}

func TestLinuxSyslog_Write_InfoPriority(t *testing.T) {
	runner := &mockCommandRunner{}
	s := &linuxSyslog{out: &bytes.Buffer{}, source: "test", runner: runner}

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

func TestLinuxSyslog_Write_WarningPriority(t *testing.T) {
	runner := &mockCommandRunner{}
	s := &linuxSyslog{out: &bytes.Buffer{}, source: "test", runner: runner}

	_, err := s.Write([]byte("[WARNING] warning message"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if runner.priority != "daemon.warning" {
		t.Errorf("expected priority 'daemon.warning', got %q", runner.priority)
	}
}

func TestLinuxSyslog_Write_ErrorPriority(t *testing.T) {
	runner := &mockCommandRunner{}
	s := &linuxSyslog{out: &bytes.Buffer{}, source: "test", runner: runner}

	_, err := s.Write([]byte("[ERROR] error message"))
	if err != nil {
		t.Fatalf("expected no err, got %v", err)
	}

	if runner.priority != "daemon.err" {
		t.Errorf("expected priority 'daemon.err', got %q", runner.priority)
	}
}

func TestLinuxSyslog_Write_DefaultsToInfo(t *testing.T) {
	runner := &mockCommandRunner{}
	s := &linuxSyslog{out: &bytes.Buffer{}, source: "test", runner: runner}

	_, err := s.Write([]byte("[DEBUG] debug message"))
	if err != nil {
		t.Fatalf("expected no err, got %v", err)
	}

	if runner.priority != "daemon.info" {
		t.Errorf("expected default priority 'daemon.info', got %q", runner.priority)
	}
}

func TestLinuxSyslog_Write_PassesSourceAndMessage(t *testing.T) {
	runner := &mockCommandRunner{}
	s := &linuxSyslog{out: &bytes.Buffer{}, source: "my-app", runner: runner}

	_, err := s.Write([]byte("[INFO] hello world"))
	if err != nil {
		t.Fatalf("expected no err, got %v", err)
	}

	if runner.source != "my-app" {
		t.Errorf("expected source 'my-app', got %q", runner.source)
	}
	if runner.message != "hello world" {
		t.Errorf("expected message 'hello world', got %q", runner.message)
	}
}

func TestLinuxSyslog_Write_ForwardsToOut(t *testing.T) {
	runner := &mockCommandRunner{}
	var out bytes.Buffer
	s := &linuxSyslog{out: &out, source: "test", runner: runner}

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

func TestLinuxSyslog_Close(t *testing.T) {
	s := &linuxSyslog{out: &bytes.Buffer{}, source: "test", runner: &mockCommandRunner{}}

	if err := s.Close(); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestNewWithRunner_Linux(t *testing.T) {
	runner := &mockCommandRunner{}
	var out bytes.Buffer

	syslogger := newWithRunner("test-source", &out, runner)

	if syslogger == nil {
		t.Fatal("expected non-nil Syslog")
	}
}
