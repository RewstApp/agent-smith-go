//go:build windows

package syslog

import (
	"bytes"
	"testing"
)

type mockEventLogger struct {
	infoMessages    []string
	warningMessages []string
	errorMessages   []string
	closed          bool
}

func (m *mockEventLogger) Info(eid uint32, msg string) error {
	m.infoMessages = append(m.infoMessages, msg)
	return nil
}

func (m *mockEventLogger) Warning(eid uint32, msg string) error {
	m.warningMessages = append(m.warningMessages, msg)
	return nil
}

func (m *mockEventLogger) Error(eid uint32, msg string) error {
	m.errorMessages = append(m.errorMessages, msg)
	return nil
}

func (m *mockEventLogger) Close() error {
	m.closed = true
	return nil
}

func TestWindowsSyslog_Write_Info(t *testing.T) {
	mock := &mockEventLogger{}
	var out bytes.Buffer
	s := &windowsSyslog{out: &out, log: mock}

	data := []byte("[INFO] hello world")
	n, err := s.Write(data)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != len(data) {
		t.Errorf("expected %d bytes written, got %d", len(data), n)
	}
	if len(mock.infoMessages) != 1 {
		t.Fatalf("expected 1 info message, got %d", len(mock.infoMessages))
	}
	if mock.infoMessages[0] != "hello world" {
		t.Errorf("expected message 'hello world', got %q", mock.infoMessages[0])
	}
	if len(mock.warningMessages) != 0 || len(mock.errorMessages) != 0 {
		t.Error("expected no warning or error messages")
	}
}

func TestWindowsSyslog_Write_Warning(t *testing.T) {
	mock := &mockEventLogger{}
	var out bytes.Buffer
	s := &windowsSyslog{out: &out, log: mock}

	data := []byte("[WARNING] disk space low")
	_, err := s.Write(data)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(mock.warningMessages) != 1 {
		t.Fatalf("expected 1 warning message, got %d", len(mock.warningMessages))
	}
	if mock.warningMessages[0] != "disk space low" {
		t.Errorf("expected message 'disk space low', got %q", mock.warningMessages[0])
	}
	if len(mock.infoMessages) != 0 || len(mock.errorMessages) != 0 {
		t.Error("expected no info or error messages")
	}
}

func TestWindowsSyslog_Write_Error(t *testing.T) {
	mock := &mockEventLogger{}
	var out bytes.Buffer
	s := &windowsSyslog{out: &out, log: mock}

	data := []byte("[ERROR] something failed")
	_, err := s.Write(data)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(mock.errorMessages) != 1 {
		t.Fatalf("expected 1 error message, got %d", len(mock.errorMessages))
	}
	if mock.errorMessages[0] != "something failed" {
		t.Errorf("expected message 'something failed', got %q", mock.errorMessages[0])
	}
	if len(mock.infoMessages) != 0 || len(mock.warningMessages) != 0 {
		t.Error("expected no info or warning messages")
	}
}

func TestWindowsSyslog_Write_ForwardsToOut(t *testing.T) {
	mock := &mockEventLogger{}
	var out bytes.Buffer
	s := &windowsSyslog{out: &out, log: mock}

	data := []byte("[INFO] forwarded message")
	s.Write(data)

	if out.String() != string(data) {
		t.Errorf("expected out to contain %q, got %q", string(data), out.String())
	}
}

func TestWindowsSyslog_Write_UsesCorrectEventIds(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantInfoId bool
		wantWarnId bool
		wantErrId  bool
	}{
		{"info event id", "[INFO] msg", true, false, false},
		{"warning event id", "[WARNING] msg", false, true, false},
		{"error event id", "[ERROR] msg", false, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockEventLogger{}
			s := &windowsSyslog{out: &bytes.Buffer{}, log: mock}
			s.Write([]byte(tt.input))

			if tt.wantInfoId && len(mock.infoMessages) == 0 {
				t.Error("expected info event")
			}
			if tt.wantWarnId && len(mock.warningMessages) == 0 {
				t.Error("expected warning event")
			}
			if tt.wantErrId && len(mock.errorMessages) == 0 {
				t.Error("expected error event")
			}
		})
	}
}

func TestWindowsSyslog_Close(t *testing.T) {
	mock := &mockEventLogger{}
	s := &windowsSyslog{out: &bytes.Buffer{}, log: mock}

	err := s.Close()

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !mock.closed {
		t.Error("expected eventLogger.Close to be called")
	}
}
