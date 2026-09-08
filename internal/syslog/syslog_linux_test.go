//go:build linux

package syslog

import (
	"bytes"
	"testing"
)

// On Linux the -t tag carries the source, so the message body is forwarded
// unchanged.
func TestLinuxSyslog_Write_PassesMessageUnchanged(t *testing.T) {
	runner := &mockCommandRunner{}
	s := &unixSyslog{out: &bytes.Buffer{}, source: "my-app", runner: runner}

	_, err := s.Write([]byte("[INFO] hello world"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if runner.message != "hello world" {
		t.Errorf("expected message 'hello world', got %q", runner.message)
	}
}

func TestSyslogMessage_Linux(t *testing.T) {
	if got := syslogMessage("my-app", "hello world"); got != "hello world" {
		t.Errorf("expected 'hello world', got %q", got)
	}
}
