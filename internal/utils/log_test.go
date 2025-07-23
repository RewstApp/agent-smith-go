package utils

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestConfigureLogger(t *testing.T) {
	buf := bytes.Buffer{}
	prefix := "TEST"
	message := "this is a test"

	logger := ConfigureLogger(prefix, &buf, Info)
	logger.Info(message)
	expectedPrefix := fmt.Sprintf("%s: ", prefix)
	output := buf.String()

	if !strings.Contains(output, expectedPrefix) {
		t.Errorf("expected prefix %q, got %s", expectedPrefix, output)
	}

	if !strings.Contains(output, message) {
		t.Errorf("expected log message to contain '%s', got %s", message, output)
	}
}
