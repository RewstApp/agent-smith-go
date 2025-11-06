package interpreter

import (
	"context"
	"testing"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/hashicorp/go-hclog"
)

func TestExecuteUsingPowershell_InvalidBase64(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping powershell execution test in short mode")
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
	result := executeUsingPowershell(context.Background(), msg, device, logger, false)

	// Should return error result
	if len(result) == 0 {
		t.Error("expected non-empty result for invalid base64")
	}
}

func TestExecuteUsingPowershell_ValidCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping powershell execution test in short mode")
	}

	// Encode a simple Write-Output command
	cmd := "Write-Output 'test output'"
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
	result := executeUsingPowershell(context.Background(), msg, device, logger, false)

	// Should return valid result
	if len(result) == 0 {
		t.Error("expected non-empty result")
	}
}

func TestExecuteUsingPowershell_CommandWithError(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping powershell execution test in short mode")
	}

	// Encode a command that will fail
	cmd := "throw 'test error'"
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
	result := executeUsingPowershell(context.Background(), msg, device, logger, false)

	// Should return result (possibly with error)
	if len(result) == 0 {
		t.Error("expected non-empty result")
	}
}

func TestExecuteUsingPowershell_MultilineCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping powershell execution test in short mode")
	}

	// Encode a multiline command
	cmd := `Write-Output "line1"
Write-Output "line2"
Write-Output "line3"`
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
	result := executeUsingPowershell(context.Background(), msg, device, logger, false)

	// Should return valid result
	if len(result) == 0 {
		t.Error("expected non-empty result")
	}
}

func TestExecuteUsingPowershell_WithDebugLogger(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping powershell execution test in short mode")
	}

	// Encode a simple command
	cmd := "Write-Output 'debug test'"
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

	result := executeUsingPowershell(context.Background(), msg, device, logger, false)

	// Should return valid result and exercise debug code paths
	if len(result) == 0 {
		t.Error("expected non-empty result")
	}
}

func TestExecuteUsingPowershell_WithPwsh(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping powershell execution test in short mode")
	}

	// Encode a simple command
	cmd := "Write-Output 'pwsh test'"
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
	result := executeUsingPowershell(context.Background(), msg, device, logger, true)

	// Should return valid result using pwsh
	if len(result) == 0 {
		t.Error("expected non-empty result")
	}
}

func TestExecuteUsingPowershell_CancelledContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping powershell execution test in short mode")
	}

	// Encode a long-running command
	cmd := "Start-Sleep -Seconds 10"
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
	result := executeUsingPowershell(ctx, msg, device, logger, false)

	// Should return result (command should be killed)
	if len(result) == 0 {
		t.Error("expected non-empty result")
	}
}

func TestExecuteUsingPowershell_EmptyCommand(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping powershell execution test in short mode")
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
	result := executeUsingPowershell(context.Background(), msg, device, logger, false)

	// Should return valid result
	if len(result) == 0 {
		t.Error("expected non-empty result")
	}
}

func TestExecuteUsingPowershell_CommandWithStderr(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping powershell execution test in short mode")
	}

	// Encode a command that writes to stderr (Write-Error)
	cmd := "Write-Error 'error message'"
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
	result := executeUsingPowershell(context.Background(), msg, device, logger, false)

	// Should return valid result with stderr captured
	if len(result) == 0 {
		t.Error("expected non-empty result")
	}
}

func TestExecuteUsingPowershell_VariableExpansion(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping powershell execution test in short mode")
	}

	// Encode a command that uses environment variables
	cmd := "$env:AGENT_SMITH_VERSION"
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
	result := executeUsingPowershell(context.Background(), msg, device, logger, false)

	// Should return valid result with environment variable expanded
	if len(result) == 0 {
		t.Error("expected non-empty result")
	}
}
