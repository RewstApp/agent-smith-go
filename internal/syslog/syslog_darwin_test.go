//go:build darwin

package syslog

import (
	"bytes"
	"testing"
)

// macOS does not surface the -t tag the way syslogd does, so the source is
// repeated in the message body.
func TestDarwinSyslog_Write_PrefixesMessageWithSource(t *testing.T) {
	runner := &mockCommandRunner{}
	s := &unixSyslog{out: &bytes.Buffer{}, source: "my-app", runner: runner}

	_, err := s.Write([]byte("[INFO] hello world"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if runner.message != "my-app: hello world" {
		t.Errorf("expected message 'my-app: hello world', got %q", runner.message)
	}
}

func TestSyslogMessage_Darwin(t *testing.T) {
	if got := syslogMessage("my-app", "hello world"); got != "my-app: hello world" {
		t.Errorf("expected 'my-app: hello world', got %q", got)
	}
}
