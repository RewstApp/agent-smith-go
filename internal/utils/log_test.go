package utils

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"testing"
)

func TestConfigureLogger(t *testing.T) {
	buf := bytes.Buffer{}
	prefix := "TEST"
	message := "this is a test"

	ConfigureLogger(prefix, &buf)
	log.Println(message)
	expectedPrefix := fmt.Sprintf("[%s] ", prefix)
	output := buf.String()

	if !strings.HasPrefix(output, expectedPrefix) {
		t.Errorf("expected prefix %q, got %s", expectedPrefix, output)
	}

	if !strings.Contains(output, message) {
		t.Errorf("expected log message to contain '%s', got %s", message, output)
	}
}
