//go:build linux

package syslog

import (
	"bytes"
	"testing"
)

func TestLinuxSyslog_Write_ForwardsToOut(t *testing.T) {
	var out bytes.Buffer
	s := &linuxSyslog{out: &out, source: "test"}

	data := []byte("[INFO] hello from linux")
	n, err := s.Write(data)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != len(data) {
		t.Errorf("expected %d bytes written, got %d", len(data), n)
	}
	if out.String() != string(data) {
		t.Errorf("expected out to contain %q, got %q", string(data), out.String())
	}
}

func TestLinuxSyslog_Write_Levels(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"info level", "[INFO] info message"},
		{"warning level", "[WARNING] warning message"},
		{"error level", "[ERROR] error message"},
		{"unknown level defaults to info", "[DEBUG] debug message"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			s := &linuxSyslog{out: &out, source: "test"}

			_, err := s.Write([]byte(tt.input))
			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if out.String() != tt.input {
				t.Errorf("expected out %q, got %q", tt.input, out.String())
			}
		})
	}
}

func TestLinuxSyslog_Close(t *testing.T) {
	s := &linuxSyslog{out: &bytes.Buffer{}, source: "test"}

	err := s.Close()
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestNew_Linux(t *testing.T) {
	var out bytes.Buffer
	syslogger, err := New("test-source", &out)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if syslogger == nil {
		t.Fatal("expected non-nil Syslog")
	}
}
