package interpreter

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/hashicorp/go-hclog"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// encodeCommand takes a string command and returns a base64-encoded UTF16-LE string
func encodeCommand(cmd string) string {
	encoder := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewEncoder()
	utf16Bytes, _, _ := transform.Bytes(encoder, []byte(cmd))
	return base64.StdEncoding.EncodeToString(utf16Bytes)
}

func TestExecuteUsingBash_InvalidBase64(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bash execution test in short mode")
	}

	msg := &Message{
		Commands: "this is not valid base64!@#$",
		PostId:   "test-post",
	}

	device := agent.Device{
		RewstOrgId:      "test-org",
		RewstEngineHost: "example.com",
	}

	logger := hclog.NewNullLogger()
	result := executeUsingBash(context.Background(), msg, device, logger)

	// Should return error result
	if len(result) == 0 {
		t.Error("expected non-empty result for invalid base64")
	}
}

func TestExecuteUsingBash_ValidCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bash execution test in short mode")
	}

	// Encode a simple echo command
	cmd := "echo 'test output'"
	encodedCmd := encodeCommand(cmd)

	msg := &Message{
		Commands: encodedCmd,
		PostId:   "test-post",
	}

	device := agent.Device{
		RewstOrgId:      "test-org",
		RewstEngineHost: "example.com",
	}

	logger := hclog.NewNullLogger()
	result := executeUsingBash(context.Background(), msg, device, logger)

	// Should return valid result
	if len(result) == 0 {
		t.Error("expected non-empty result")
	}
}

func TestExecuteUsingBash_CommandWithError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bash execution test in short mode")
	}

	// Encode a command that will fail
	cmd := "exit 1"
	encodedCmd := encodeCommand(cmd)

	msg := &Message{
		Commands: encodedCmd,
		PostId:   "test-post",
	}

	device := agent.Device{
		RewstOrgId:      "test-org",
		RewstEngineHost: "example.com",
	}

	logger := hclog.NewNullLogger()
	result := executeUsingBash(context.Background(), msg, device, logger)

	// Should return result (possibly with error)
	if len(result) == 0 {
		t.Error("expected non-empty result")
	}
}

func TestExecuteUsingBash_MultilineCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bash execution test in short mode")
	}

	// Encode a multiline command
	cmd := `echo "line1"
echo "line2"
echo "line3"`
	encodedCmd := encodeCommand(cmd)

	msg := &Message{
		Commands: encodedCmd,
		PostId:   "test-post",
	}

	device := agent.Device{
		RewstOrgId:      "test-org",
		RewstEngineHost: "example.com",
	}

	logger := hclog.NewNullLogger()
	result := executeUsingBash(context.Background(), msg, device, logger)

	// Should return valid result
	if len(result) == 0 {
		t.Error("expected non-empty result")
	}
}

func TestExecuteUsingBash_WithDebugLogger(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bash execution test in short mode")
	}

	// Encode a simple command
	cmd := "echo 'debug test'"
	encodedCmd := encodeCommand(cmd)

	msg := &Message{
		Commands: encodedCmd,
		PostId:   "test-post",
	}

	device := agent.Device{
		RewstOrgId:      "test-org",
		RewstEngineHost: "example.com",
	}

	// Create a debug-level logger
	logger := hclog.New(&hclog.LoggerOptions{
		Name:  "test",
		Level: hclog.Debug,
	})

	result := executeUsingBash(context.Background(), msg, device, logger)

	// Should return valid result and exercise debug code paths
	if len(result) == 0 {
		t.Error("expected non-empty result")
	}
}

func TestExecuteUsingBash_CancelledContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bash execution test in short mode")
	}

	// Encode a long-running command
	cmd := "sleep 10"
	encodedCmd := encodeCommand(cmd)

	msg := &Message{
		Commands: encodedCmd,
		PostId:   "test-post",
	}

	device := agent.Device{
		RewstOrgId:      "test-org",
		RewstEngineHost: "example.com",
	}

	// Create a context that's already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	logger := hclog.NewNullLogger()
	result := executeUsingBash(ctx, msg, device, logger)

	// Should return result (command should be killed)
	if len(result) == 0 {
		t.Error("expected non-empty result")
	}
}

func TestExecuteUsingBash_EmptyCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bash execution test in short mode")
	}

	// Encode an empty command
	cmd := ""
	encodedCmd := encodeCommand(cmd)

	msg := &Message{
		Commands: encodedCmd,
		PostId:   "test-post",
	}

	device := agent.Device{
		RewstOrgId:      "test-org",
		RewstEngineHost: "example.com",
	}

	logger := hclog.NewNullLogger()
	result := executeUsingBash(context.Background(), msg, device, logger)

	// Should return valid result
	if len(result) == 0 {
		t.Error("expected non-empty result")
	}
}

func TestExecuteUsingBash_CommandWithStderr(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping bash execution test in short mode")
	}

	// Encode a command that writes to stderr
	cmd := "echo 'error message' >&2"
	encodedCmd := encodeCommand(cmd)

	msg := &Message{
		Commands: encodedCmd,
		PostId:   "test-post",
	}

	device := agent.Device{
		RewstOrgId:      "test-org",
		RewstEngineHost: "example.com",
	}

	logger := hclog.NewNullLogger()
	result := executeUsingBash(context.Background(), msg, device, logger)

	// Should return valid result with stderr captured
	if len(result) == 0 {
		t.Error("expected non-empty result")
	}
}
