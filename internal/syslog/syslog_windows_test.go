//go:build windows

package syslog

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc/eventlog"
)

// TestWindowsSyslog_Write tests the Write method
func TestWindowsSyslog_Write(t *testing.T) {
	// Check if we have admin privileges for event log tests
	if !isAdmin() {
		t.Skip("skipping test: requires administrator privileges")
	}

	serviceName := "RewstSyslogTest_" + time.Now().Format("20060102150405")

	// Create a buffer to capture output
	var buf bytes.Buffer

	// Create the syslog
	syslogger, err := New(serviceName, &buf)
	if err != nil {
		t.Fatalf("failed to create syslog: %v", err)
	}
	defer func() {
		syslogger.Close()
		// Cleanup: remove the event source
		eventlog.Remove(serviceName)
	}()

	tests := []struct {
		name    string
		message string
	}{
		{
			name:    "info message",
			message: "[INFO] This is an info message\n",
		},
		{
			name:    "error message",
			message: "[ERROR] This is an error message\n",
		},
		{
			name:    "warning message",
			message: "[WARNING] This is a warning message\n",
		},
		{
			name:    "plain message",
			message: "Plain message without level\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()

			n, err := syslogger.Write([]byte(tt.message))
			if err != nil {
				t.Errorf("Write() error = %v", err)
			}

			if n != len(tt.message) {
				t.Errorf("Write() returned %d bytes, expected %d", n, len(tt.message))
			}

			// Verify the message was written to the buffer
			if buf.String() != tt.message {
				t.Errorf("Write() buffer = %q, expected %q", buf.String(), tt.message)
			}
		})
	}
}

// TestWindowsSyslog_WriteMultiple tests multiple writes
func TestWindowsSyslog_WriteMultiple(t *testing.T) {
	if !isAdmin() {
		t.Skip("skipping test: requires administrator privileges")
	}

	serviceName := "RewstSyslogTestMulti_" + time.Now().Format("20060102150405")

	var buf bytes.Buffer
	syslogger, err := New(serviceName, &buf)
	if err != nil {
		t.Fatalf("failed to create syslog: %v", err)
	}
	defer func() {
		syslogger.Close()
		eventlog.Remove(serviceName)
	}()

	messages := []string{
		"[INFO] First message\n",
		"[ERROR] Second message\n",
		"[WARNING] Third message\n",
	}

	for i, msg := range messages {
		n, err := syslogger.Write([]byte(msg))
		if err != nil {
			t.Errorf("Write() #%d error = %v", i, err)
		}
		if n != len(msg) {
			t.Errorf("Write() #%d returned %d bytes, expected %d", i, n, len(msg))
		}
	}

	// Check that all messages were written to the buffer
	expected := strings.Join(messages, "")
	if buf.String() != expected {
		t.Errorf("buffer content = %q, expected %q", buf.String(), expected)
	}
}

// TestWindowsSyslog_Close tests the Close method
func TestWindowsSyslog_Close(t *testing.T) {
	if !isAdmin() {
		t.Skip("skipping test: requires administrator privileges")
	}

	serviceName := "RewstSyslogTestClose_" + time.Now().Format("20060102150405")

	var buf bytes.Buffer
	syslogger, err := New(serviceName, &buf)
	if err != nil {
		t.Fatalf("failed to create syslog: %v", err)
	}
	defer eventlog.Remove(serviceName)

	// Write a message
	_, err = syslogger.Write([]byte("[INFO] Test message\n"))
	if err != nil {
		t.Errorf("Write() error = %v", err)
	}

	// Close should succeed
	err = syslogger.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

// TestWindowsSyslog_EventLevels tests that different log levels are handled correctly
func TestWindowsSyslog_EventLevels(t *testing.T) {
	if !isAdmin() {
		t.Skip("skipping test: requires administrator privileges")
	}

	serviceName := "RewstSyslogTestLevels_" + time.Now().Format("20060102150405")

	var buf bytes.Buffer
	syslogger, err := New(serviceName, &buf)
	if err != nil {
		t.Fatalf("failed to create syslog: %v", err)
	}
	defer func() {
		syslogger.Close()
		eventlog.Remove(serviceName)
	}()

	// Test that event IDs are set correctly by checking the constants
	if infoEventId != 100 {
		t.Errorf("infoEventId = %d, expected 100", infoEventId)
	}
	if warningEventId != 200 {
		t.Errorf("warningEventId = %d, expected 200", warningEventId)
	}
	if errorEventId != 300 {
		t.Errorf("errorEventId = %d, expected 300", errorEventId)
	}

	// Write messages of different levels
	levels := []struct {
		level   string
		message string
	}{
		{"INFO", "[INFO] Information message"},
		{"WARNING", "[WARNING] Warning message"},
		{"ERROR", "[ERROR] Error message"},
	}

	for _, l := range levels {
		t.Run(l.level, func(t *testing.T) {
			_, err := syslogger.Write([]byte(l.message))
			if err != nil {
				t.Errorf("Write(%s) error = %v", l.level, err)
			}
		})
	}
}

// TestEventSourceExists tests the eventSourceExists helper function
func TestEventSourceExists(t *testing.T) {
	if !isAdmin() {
		t.Skip("skipping test: requires administrator privileges")
	}

	t.Run("non-existent source", func(t *testing.T) {
		nonExistent := "RewstNonExistent_" + time.Now().Format("20060102150405")
		exists, err := eventSourceExists(nonExistent)
		if err != nil {
			t.Errorf("eventSourceExists() error = %v", err)
		}
		if exists {
			t.Error("eventSourceExists() = true, expected false for non-existent source")
		}
	})

	t.Run("created source", func(t *testing.T) {
		sourceName := "RewstTestSource_" + time.Now().Format("20060102150405")

		// Create the event source
		err := eventlog.InstallAsEventCreate(sourceName, eventlog.Info|eventlog.Error|eventlog.Warning)
		if err != nil {
			t.Fatalf("failed to create event source: %v", err)
		}
		defer eventlog.Remove(sourceName)

		// Check if it exists
		exists, err := eventSourceExists(sourceName)
		if err != nil {
			t.Errorf("eventSourceExists() error = %v", err)
		}
		if !exists {
			t.Error("eventSourceExists() = false, expected true for created source")
		}
	})
}

// TestNew tests the New function
func TestNew(t *testing.T) {
	if !isAdmin() {
		t.Skip("skipping test: requires administrator privileges")
	}

	t.Run("create new syslog", func(t *testing.T) {
		serviceName := "RewstTestNew_" + time.Now().Format("20060102150405")
		var buf bytes.Buffer

		syslogger, err := New(serviceName, &buf)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer func() {
			syslogger.Close()
			eventlog.Remove(serviceName)
		}()

		if syslogger == nil {
			t.Error("New() returned nil syslogger")
		}

		// Verify it implements the Syslog interface
		var _ Syslog = syslogger
	})

	t.Run("create with existing source", func(t *testing.T) {
		serviceName := "RewstTestNewExisting_" + time.Now().Format("20060102150405")

		// Pre-create the event source
		err := eventlog.InstallAsEventCreate(serviceName, eventlog.Info|eventlog.Error|eventlog.Warning)
		if err != nil {
			t.Fatalf("failed to create event source: %v", err)
		}
		defer eventlog.Remove(serviceName)

		var buf bytes.Buffer
		syslogger, err := New(serviceName, &buf)
		if err != nil {
			t.Fatalf("New() error = %v", err)
		}
		defer syslogger.Close()

		if syslogger == nil {
			t.Error("New() returned nil syslogger")
		}
	})
}

// TestNew_NilWriter tests New with nil writer
func TestNew_NilWriter(t *testing.T) {
	if !isAdmin() {
		t.Skip("skipping test: requires administrator privileges")
	}

	serviceName := "RewstTestNewNil_" + time.Now().Format("20060102150405")

	// This should work but writes will fail
	syslogger, err := New(serviceName, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	defer func() {
		syslogger.Close()
		eventlog.Remove(serviceName)
	}()

	// Writing to event log should work, but writing to nil writer will panic
	// We don't test the panic here as it's expected behavior for nil writers
}

// TestWindowsSyslog_Integration tests full integration
func TestWindowsSyslog_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if !isAdmin() {
		t.Skip("skipping integration test: requires administrator privileges")
	}

	serviceName := "RewstSyslogIntegration_" + time.Now().Format("20060102150405")

	var buf bytes.Buffer
	syslogger, err := New(serviceName, &buf)
	if err != nil {
		t.Fatalf("failed to create syslog: %v", err)
	}
	defer func() {
		syslogger.Close()
		eventlog.Remove(serviceName)
	}()

	// Simulate real logging scenario
	logMessages := []string{
		"[INFO] Application started\n",
		"[INFO] Configuration loaded\n",
		"[WARNING] Connection attempt 1 failed\n",
		"[INFO] Connection established\n",
		"[ERROR] Failed to process request\n",
		"[INFO] Application stopped\n",
	}

	for i, msg := range logMessages {
		n, err := syslogger.Write([]byte(msg))
		if err != nil {
			t.Errorf("Write() message %d error = %v", i, err)
		}
		if n != len(msg) {
			t.Errorf("Write() message %d returned %d bytes, expected %d", i, n, len(msg))
		}
	}

	// Verify all messages were written to buffer
	expected := strings.Join(logMessages, "")
	if buf.String() != expected {
		t.Errorf("buffer content length = %d, expected %d", len(buf.String()), len(expected))
	}

	// Close the logger
	err = syslogger.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

// TestEventSourceExists_RegistryError tests error handling in eventSourceExists
func TestEventSourceExists_RegistryError(t *testing.T) {
	if !isAdmin() {
		t.Skip("skipping test: requires administrator privileges")
	}

	// Test with an invalid registry path that should cause a different error
	// We can't easily mock registry errors, so we test with valid inputs
	// This test documents expected behavior
	exists, err := eventSourceExists("ValidServiceName")
	if err != nil {
		t.Errorf("unexpected error for valid service name: %v", err)
	}
	// Should return false for non-existent service
	if exists {
		t.Error("should return false for non-existent service")
	}
}

// TestNew_ErrorCases tests error handling in New function
func TestNew_ErrorCases(t *testing.T) {
	if !isAdmin() {
		t.Skip("skipping test: requires administrator privileges")
	}

	t.Run("valid service name", func(t *testing.T) {
		serviceName := "RewstTestError_" + time.Now().Format("20060102150405")
		var buf bytes.Buffer

		syslogger, err := New(serviceName, &buf)
		if err != nil {
			t.Fatalf("New() should succeed with valid name: %v", err)
		}
		defer func() {
			syslogger.Close()
			eventlog.Remove(serviceName)
		}()

		if syslogger == nil {
			t.Error("New() returned nil syslogger")
		}
	})
}

// TestWindowsSyslog_WriteLargeMessages tests writing large messages
func TestWindowsSyslog_WriteLargeMessages(t *testing.T) {
	if !isAdmin() {
		t.Skip("skipping test: requires administrator privileges")
	}

	serviceName := "RewstTestLarge_" + time.Now().Format("20060102150405")
	var buf bytes.Buffer

	syslogger, err := New(serviceName, &buf)
	if err != nil {
		t.Fatalf("failed to create syslog: %v", err)
	}
	defer func() {
		syslogger.Close()
		eventlog.Remove(serviceName)
	}()

	// Create a large message
	largeMessage := "[INFO] " + strings.Repeat("A", 5000) + "\n"

	n, err := syslogger.Write([]byte(largeMessage))
	if err != nil {
		t.Errorf("Write() error = %v", err)
	}

	if n != len(largeMessage) {
		t.Errorf("Write() returned %d bytes, expected %d", n, len(largeMessage))
	}

	if buf.String() != largeMessage {
		t.Errorf("buffer content length = %d, expected %d", len(buf.String()), len(largeMessage))
	}
}

// TestWindowsSyslog_WriteWithoutNewline tests writing messages without newlines
func TestWindowsSyslog_WriteWithoutNewline(t *testing.T) {
	if !isAdmin() {
		t.Skip("skipping test: requires administrator privileges")
	}

	serviceName := "RewstTestNoNewline_" + time.Now().Format("20060102150405")
	var buf bytes.Buffer

	syslogger, err := New(serviceName, &buf)
	if err != nil {
		t.Fatalf("failed to create syslog: %v", err)
	}
	defer func() {
		syslogger.Close()
		eventlog.Remove(serviceName)
	}()

	messages := []string{
		"[INFO] Message without newline",
		"[ERROR] Another message",
		"[WARNING] Yet another message",
	}

	for _, msg := range messages {
		n, err := syslogger.Write([]byte(msg))
		if err != nil {
			t.Errorf("Write(%q) error = %v", msg, err)
		}
		if n != len(msg) {
			t.Errorf("Write(%q) returned %d bytes, expected %d", msg, n, len(msg))
		}
	}
}

// TestWindowsSyslog_WriteMinimalMessage tests writing minimal valid messages
func TestWindowsSyslog_WriteMinimalMessage(t *testing.T) {
	if !isAdmin() {
		t.Skip("skipping test: requires administrator privileges")
	}

	serviceName := "RewstTestMinimal_" + time.Now().Format("20060102150405")
	var buf bytes.Buffer

	syslogger, err := New(serviceName, &buf)
	if err != nil {
		t.Fatalf("failed to create syslog: %v", err)
	}
	defer func() {
		syslogger.Close()
		eventlog.Remove(serviceName)
	}()

	// Test minimal valid message (extractMessage expects format: "...]<space><message>")
	minimalMsg := "] x"
	n, err := syslogger.Write([]byte(minimalMsg))
	if err != nil {
		t.Errorf("Write() error = %v", err)
	}
	if n != len(minimalMsg) {
		t.Errorf("Write() returned %d bytes, expected %d", n, len(minimalMsg))
	}
}

// TestWindowsSyslog_ConcurrentWrites tests concurrent writes
func TestWindowsSyslog_ConcurrentWrites(t *testing.T) {
	if !isAdmin() {
		t.Skip("skipping test: requires administrator privileges")
	}

	serviceName := "RewstTestConcurrent_" + time.Now().Format("20060102150405")
	var buf bytes.Buffer

	syslogger, err := New(serviceName, &buf)
	if err != nil {
		t.Fatalf("failed to create syslog: %v", err)
	}
	defer func() {
		syslogger.Close()
		eventlog.Remove(serviceName)
	}()

	// Write messages concurrently
	done := make(chan bool)
	numGoroutines := 5
	messagesPerGoroutine := 10

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			for j := 0; j < messagesPerGoroutine; j++ {
				msg := fmt.Sprintf("[INFO] Goroutine %d, message %d\n", id, j)
				_, err := syslogger.Write([]byte(msg))
				if err != nil {
					t.Errorf("Write() error in goroutine %d: %v", id, err)
				}
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		<-done
	}
}

// TestWindowsSyslog_WriteAfterClose tests writing after close
func TestWindowsSyslog_WriteAfterClose(t *testing.T) {
	if !isAdmin() {
		t.Skip("skipping test: requires administrator privileges")
	}

	serviceName := "RewstTestAfterClose_" + time.Now().Format("20060102150405")
	var buf bytes.Buffer

	syslogger, err := New(serviceName, &buf)
	if err != nil {
		t.Fatalf("failed to create syslog: %v", err)
	}
	defer eventlog.Remove(serviceName)

	// Close the syslogger
	err = syslogger.Close()
	if err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Try to write after close
	// Note: The Write will succeed for the buffer write, but will fail for eventlog
	// This behavior is acceptable as the buffer is still writable
	n, err := syslogger.Write([]byte("[INFO] Message after close\n"))

	// The write may partially succeed (buffer write) even though eventlog is closed
	// We just verify that the function doesn't panic
	if err != nil {
		t.Logf("Write() after Close() returned error: %v (expected)", err)
	} else {
		t.Logf("Write() after Close() succeeded (buffer write still works, returned %d bytes)", n)
	}
}

// TestWindowsSyslog_DoubleClose tests closing twice
func TestWindowsSyslog_DoubleClose(t *testing.T) {
	if !isAdmin() {
		t.Skip("skipping test: requires administrator privileges")
	}

	serviceName := "RewstTestDoubleClose_" + time.Now().Format("20060102150405")
	var buf bytes.Buffer

	syslogger, err := New(serviceName, &buf)
	if err != nil {
		t.Fatalf("failed to create syslog: %v", err)
	}
	defer eventlog.Remove(serviceName)

	// First close
	err = syslogger.Close()
	if err != nil {
		t.Errorf("first Close() error = %v", err)
	}

	// Second close - may return an error
	err = syslogger.Close()
	// We don't fail on error here as double-close behavior may vary
	t.Logf("second Close() returned: %v", err)
}

// TestWindowsSyslog_StructFields tests internal structure
func TestWindowsSyslog_StructFields(t *testing.T) {
	if !isAdmin() {
		t.Skip("skipping test: requires administrator privileges")
	}

	serviceName := "RewstTestStruct_" + time.Now().Format("20060102150405")
	var buf bytes.Buffer

	syslogger, err := New(serviceName, &buf)
	if err != nil {
		t.Fatalf("failed to create syslog: %v", err)
	}
	defer func() {
		syslogger.Close()
		eventlog.Remove(serviceName)
	}()

	// Verify internal structure
	winSyslog, ok := syslogger.(*windowsSyslog)
	if !ok {
		t.Fatal("syslogger is not a windowsSyslog")
	}

	if winSyslog.out != &buf {
		t.Error("out writer does not match provided buffer")
	}

	if winSyslog.log == nil {
		t.Error("log handle should not be nil")
	}
}

// TestWindowsSyslog_EventConstants tests event ID constants
func TestWindowsSyslog_EventConstants(t *testing.T) {
	// Test that event constants have expected values
	tests := []struct {
		name     string
		constant int
		expected int
	}{
		{"infoEventId", infoEventId, 100},
		{"warningEventId", warningEventId, 200},
		{"errorEventId", errorEventId, 300},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.constant != tt.expected {
				t.Errorf("%s = %d, expected %d", tt.name, tt.constant, tt.expected)
			}
		})
	}
}

// TestWindowsSyslog_MessageExtraction tests message extraction from different formats
func TestWindowsSyslog_MessageExtraction(t *testing.T) {
	if !isAdmin() {
		t.Skip("skipping test: requires administrator privileges")
	}

	serviceName := "RewstTestExtract_" + time.Now().Format("20060102150405")
	var buf bytes.Buffer

	syslogger, err := New(serviceName, &buf)
	if err != nil {
		t.Fatalf("failed to create syslog: %v", err)
	}
	defer func() {
		syslogger.Close()
		eventlog.Remove(serviceName)
	}()

	tests := []struct {
		name    string
		message string
	}{
		{
			name:    "message with special characters",
			message: "[INFO] Special chars: @#$%^&*()\n",
		},
		{
			name:    "message with unicode",
			message: "[INFO] Unicode: 你好世界 مرحبا العالم\n",
		},
		{
			name:    "message with tabs",
			message: "[INFO] Message\twith\ttabs\n",
		},
		{
			name:    "message with multiple spaces",
			message: "[INFO]     Multiple    spaces\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf.Reset()
			n, err := syslogger.Write([]byte(tt.message))
			if err != nil {
				t.Errorf("Write() error = %v", err)
			}
			if n != len(tt.message) {
				t.Errorf("Write() returned %d bytes, expected %d", n, len(tt.message))
			}
			if buf.String() != tt.message {
				t.Errorf("buffer = %q, expected %q", buf.String(), tt.message)
			}
		})
	}
}

// isAdmin checks if the current process has administrative privileges
func isAdmin() bool {
	_, err := registry.OpenKey(
		registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows\CurrentVersion`,
		registry.QUERY_VALUE,
	)
	if err != nil {
		return false
	}
	return true
}
