package utils

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

func TestConfigureLogger(t *testing.T) {
	var buf bytes.Buffer

	// Set up logger with prefix and buffer
	ConfigureLogger("TEST", &buf)

	// Write a test log message
	log.Println("This is a test")

	output := buf.String()

	// Check prefix
	expectedPrefix := "[TEST] "
	if !strings.HasPrefix(output, expectedPrefix) {
		t.Errorf("Expected prefix %q, but got: %s", expectedPrefix, output)
	}

	// Check that the message is included
	if !strings.Contains(output, "This is a test") {
		t.Errorf("Expected log message to contain 'This is a test', got: %s", output)
	}
}
