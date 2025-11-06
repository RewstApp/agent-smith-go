package interpreter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/hashicorp/go-hclog"
)

func TestMessage_CreatePostbackRequest(t *testing.T) {
	msg := Message{
		PostId: "id:segment",
	}
	device := agent.Device{
		RewstEngineHost: "example.com",
	}
	body := bytes.NewBufferString(`{"result":"ok"}`)
	req, err := msg.CreatePostbackRequest(context.Background(), device, body)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	expectedUrl := "https://example.com/webhooks/custom/action/id/segment"
	if req.URL.String() != expectedUrl {
		t.Errorf("expected URL %s, got %s", expectedUrl, req.URL.String())
	}
	if req.Method != http.MethodPost {
		t.Errorf("expected POST method, got %s", req.Method)
	}
	if req.Header.Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", req.Header.Get("Content-Type"))
	}
}

func TestMessage_CreatePostbackRequest_MultipleColons(t *testing.T) {
	msg := Message{
		PostId: "id:segment:another:part",
	}
	device := agent.Device{
		RewstEngineHost: "example.com",
	}
	body := bytes.NewBufferString(`{"result":"ok"}`)
	req, err := msg.CreatePostbackRequest(context.Background(), device, body)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	expectedUrl := "https://example.com/webhooks/custom/action/id/segment/another/part"
	if req.URL.String() != expectedUrl {
		t.Errorf("expected URL %s, got %s", expectedUrl, req.URL.String())
	}
}

func TestErrorResultBytes(t *testing.T) {
	err := errors.New("test error")
	result := errorResultBytes(err)

	var out errorResult
	json.Unmarshal(result, &out)
	if out.Error != "test error" {
		t.Errorf("expected 'test error', got %s", out.Error)
	}

	// Test it returns valid JSON
	if !json.Valid(result) {
		t.Error("expected valid JSON")
	}
}

func TestResultBytes(t *testing.T) {
	tests := []struct {
		name   string
		result *result
	}{
		{
			name:   "success result",
			result: &result{Output: "success output", Error: ""},
		},
		{
			name:   "error result",
			result: &result{Output: "", Error: "error message"},
		},
		{
			name:   "both output and error",
			result: &result{Output: "some output", Error: "some error"},
		},
		{
			name:   "empty result",
			result: &result{Output: "", Error: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resultBytes := resultBytes(tt.result)

			// Should return valid JSON
			if !json.Valid(resultBytes) {
				t.Error("expected valid JSON")
			}

			// Should be able to unmarshal back
			var unmarshaled result
			err := json.Unmarshal(resultBytes, &unmarshaled)
			if err != nil {
				t.Errorf("failed to unmarshal: %v", err)
			}

			if unmarshaled.Output != tt.result.Output {
				t.Errorf("expected output %q, got %q", tt.result.Output, unmarshaled.Output)
			}
			if unmarshaled.Error != tt.result.Error {
				t.Errorf("expected error %q, got %q", tt.result.Error, unmarshaled.Error)
			}
		})
	}
}

func TestMessageCustomUnmarshal(t *testing.T) {
	var msg Message
	var err error

	err = json.Unmarshal([]byte("{}"), &msg)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if msg.InterpreterOverride.Value != "" {
		t.Errorf("expected '', got '%v'", msg.InterpreterOverride.Value)
	}

	err = json.Unmarshal([]byte("{\"interpreter_override\":false}"), &msg)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if msg.InterpreterOverride.Value != "" {
		t.Errorf("expected '', got '%v'", msg.InterpreterOverride.Value)
	}

	err = json.Unmarshal([]byte("{\"interpreter_override\":true}"), &msg)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if msg.InterpreterOverride.Value != "true" {
		t.Errorf("expected 'true', got '%v'", msg.InterpreterOverride.Value)
	}

	err = json.Unmarshal([]byte("{\"interpreter_override\":\"test\"}"), &msg)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if msg.InterpreterOverride.Value != "test" {
		t.Errorf("expected 'test', got '%v'", msg.InterpreterOverride.Value)
	}

	err = json.Unmarshal([]byte("{\"interpreter_override\":\"\"}"), &msg)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if msg.InterpreterOverride.Value != "" {
		t.Errorf("expected '', got '%v'", msg.InterpreterOverride.Value)
	}
}

func TestStringFalse_UnmarshalJSON_InvalidType(t *testing.T) {
	var sf StringFalse
	err := json.Unmarshal([]byte("123"), &sf)
	if err == nil {
		t.Error("expected error for invalid type, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported type") {
		t.Errorf("expected 'unsupported type' error, got %v", err)
	}
}

func TestMessage_Parse(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		wantErr bool
	}{
		{
			name:    "valid message with commands",
			data:    `{"post_id":"test123","commands":"echo hello","get_installation":false}`,
			wantErr: false,
		},
		{
			name:    "valid message with get_installation",
			data:    `{"post_id":"test456","get_installation":true}`,
			wantErr: false,
		},
		{
			name:    "empty message",
			data:    `{}`,
			wantErr: false,
		},
		{
			name:    "invalid JSON",
			data:    `{invalid json}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var msg Message
			err := msg.Parse([]byte(tt.data))
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestMessage_Execute_NoOp(t *testing.T) {
	msg := Message{
		Commands:        "",
		GetInstallation: false,
	}

	device := agent.Device{
		RewstOrgId: "test-org",
	}

	logger := hclog.NewNullLogger()
	result := msg.Execute(context.Background(), device, logger)

	// Should return error for noop
	var errorResult errorResult
	err := json.Unmarshal(result, &errorResult)
	if err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if !strings.Contains(errorResult.Error, "noop") {
		t.Errorf("expected noop error, got %s", errorResult.Error)
	}
}

func TestMessage_Execute_WithCommands_Windows(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping execution test in short mode")
	}

	msg := Message{
		Commands: "echo test",
		PostId:   "test-post-id",
	}

	device := agent.Device{
		RewstOrgId:      "test-org",
		RewstEngineHost: "example.com",
	}

	logger := hclog.NewNullLogger()
	result := msg.Execute(context.Background(), device, logger)

	// Result should be valid JSON
	if !json.Valid(result) {
		t.Error("expected valid JSON result")
	}
}

func TestMessage_Execute_InterpreterSelection(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping execution test in short mode")
	}

	tests := []struct {
		name                string
		interpreterOverride string
		commands            string
	}{
		{
			name:                "powershell override",
			interpreterOverride: "powershell",
			commands:            "Write-Output 'test'",
		},
		{
			name:                "pwsh override",
			interpreterOverride: "pwsh",
			commands:            "Write-Output 'test'",
		},
		{
			name:                "bash override",
			interpreterOverride: "bash",
			commands:            "echo test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := Message{
				Commands: tt.commands,
				PostId:   "test-post",
				InterpreterOverride: StringFalse{
					Value: tt.interpreterOverride,
				},
			}

			device := agent.Device{
				RewstOrgId:      "test-org",
				RewstEngineHost: "example.com",
			}

			logger := hclog.NewNullLogger()
			result := msg.Execute(context.Background(), device, logger)

			// Result should be valid JSON
			if !json.Valid(result) {
				t.Errorf("expected valid JSON result for interpreter %s", tt.interpreterOverride)
			}
		})
	}
}

func TestCommandsResult_Structure(t *testing.T) {
	cr := CommandsResult{
		Interpreter:  "powershell",
		TempFilename: "temp.ps1",
		ExitCode:     0,
		Stderr:       "",
		Stdout:       "output",
	}

	if cr.Interpreter != "powershell" {
		t.Errorf("expected interpreter powershell, got %s", cr.Interpreter)
	}
	if cr.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", cr.ExitCode)
	}
}

func TestGetInstallationResult_Structure(t *testing.T) {
	gir := GetInstallationResult{
		StatusCode: 200,
	}

	if gir.StatusCode != 200 {
		t.Errorf("expected status code 200, got %d", gir.StatusCode)
	}
}

func TestResult_Structure(t *testing.T) {
	r := Result{
		PostId: "test-id",
		Commands: &CommandsResult{
			Interpreter: "bash",
			ExitCode:    0,
		},
		GetInstallation: &GetInstallationResult{
			StatusCode: 200,
		},
	}

	if r.PostId != "test-id" {
		t.Errorf("expected post id test-id, got %s", r.PostId)
	}
	if r.Commands.Interpreter != "bash" {
		t.Errorf("expected bash interpreter, got %s", r.Commands.Interpreter)
	}
}

func TestMessage_AllFields(t *testing.T) {
	jsonData := `{
		"post_id": "test123",
		"commands": "echo hello",
		"interpreter_override": "bash",
		"get_installation": true,
		"type": "command",
		"content": "test content"
	}`

	var msg Message
	err := msg.Parse([]byte(jsonData))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if msg.PostId != "test123" {
		t.Errorf("expected post_id test123, got %s", msg.PostId)
	}
	if msg.Commands != "echo hello" {
		t.Errorf("expected commands 'echo hello', got %s", msg.Commands)
	}
	if msg.InterpreterOverride.Value != "bash" {
		t.Errorf("expected interpreter_override bash, got %s", msg.InterpreterOverride.Value)
	}
	if !msg.GetInstallation {
		t.Error("expected get_installation true")
	}
	if msg.Type != "command" {
		t.Errorf("expected type command, got %s", msg.Type)
	}
	if msg.Content != "test content" {
		t.Errorf("expected content 'test content', got %s", msg.Content)
	}
}
