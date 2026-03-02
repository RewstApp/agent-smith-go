// go build:windows
package interpreter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/hashicorp/go-hclog"
)

func TestMessage_Execute_Commands(t *testing.T) {
	logger := hclog.NewNullLogger()
	executor := NewExecutor()
	command := "Write-Output 'hello from test'"

	msg := Message{
		PostId:   "test:123",
		Commands: encodeCommand(command),
	}
	device := agent.Device{RewstOrgId: "test-org"}

	resultBytes := msg.Execute(executor, context.Background(), device, logger, nil, nil)

	var out result
	err := json.Unmarshal(resultBytes, &out)
	if err != nil {
		t.Fatalf("expected valid JSON result, got %v", err)
	}

	if !strings.Contains(out.Output, "hello from test") {
		t.Errorf("expected output to contain 'hello from test', got %s", out.Output)
	}
}

func TestMessage_Execute_CommandError(t *testing.T) {
	logger := hclog.NewNullLogger()
	executor := NewExecutor()
	// command = "echo 'fail' >&2; exit 1"
	command := "[Console]::Error.WriteLine('fail'); exit 1"

	msg := Message{
		PostId:   "test:456",
		Commands: encodeCommand(command),
	}
	device := agent.Device{RewstOrgId: "test-org"}

	resultBytes := msg.Execute(executor, context.Background(), device, logger, nil, nil)

	var out result
	err := json.Unmarshal(resultBytes, &out)
	if err != nil {
		t.Fatalf("expected valid JSON result, got %v", err)
	}

	if !strings.Contains(out.Error, "fail") {
		t.Errorf("expected stderr to contain 'fail', got %s", out.Error)
	}
}

func TestMessage_Execute_InterpreterOverride(t *testing.T) {
	logger := hclog.NewNullLogger()
	override := "pwsh"
	command := "Write-Output 'ps-test'"

	msg := Message{
		PostId:              "test:789",
		Commands:            encodeCommand(command),
		InterpreterOverride: StringFalse{Value: override},
	}
	device := agent.Device{RewstOrgId: "test-org"}
	executor := NewExecutor()

	resultBytes := msg.Execute(executor, context.Background(), device, logger, nil, nil)

	var out result
	err := json.Unmarshal(resultBytes, &out)
	if err != nil {
		t.Fatalf("expected valid JSON result, got %v", err)
	}

	expected := "ps-test"

	if !strings.Contains(out.Output, expected) {
		t.Errorf("expected output to contain '%s', got %s", expected, out.Output)
	}
}
