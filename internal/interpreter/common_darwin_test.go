// go build:darwin
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
	command := "echo 'hello from test'"

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
	command := "echo 'fail' >&2; exit 1"

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
	override := "bash"
	command := "echo 'bash-test'"

	msg := Message{
		PostId:              "test:789",
		Commands:            encodeCommand(command),
		InterpreterOverride: StringFalse{Value: override},
	}
	device := agent.Device{RewstOrgId: "test-org"}

	resultBytes := msg.Execute(nil, context.Background(), device, logger, nil, nil)

	var out result
	err := json.Unmarshal(resultBytes, &out)
	if err != nil {
		t.Fatalf("expected valid JSON result, got %v", err)
	}

	expected := "bash-test"

	if !strings.Contains(out.Output, expected) {
		t.Errorf("expected output to contain '%s', got %s", expected, out.Output)
	}
}

func TestMessage_Execute_Bash(t *testing.T) {
	logger := hclog.NewNullLogger()
	msg := Message{
		PostId:              "test:bash",
		Commands:            encodeCommand("echo 'hello from bash'"),
		InterpreterOverride: StringFalse{Value: "bash"},
	}
	executor := NewBashExecutor()
	device := agent.Device{RewstOrgId: "test-org"}

	resultBytes := msg.Execute(executor, context.Background(), device, logger, nil, nil)

	var out result
	err := json.Unmarshal(resultBytes, &out)
	if err != nil {
		t.Fatalf("expected valid JSON result, got %v", err)
	}

	if !strings.Contains(out.Output, "hello from bash") {
		t.Errorf("expected output to contain 'hello from bash', got %s", out.Output)
	}

	if out.Error != "" {
		t.Errorf("expected no error, got %s", out.Error)
	}
}
