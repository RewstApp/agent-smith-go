package interpreter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/RewstApp/agent-smith-go/internal/agent"
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

func TestErrorResultBytes(t *testing.T) {
	err := errors.New("test error")
	result := errorResultBytes(err)

	var out errorResult
	json.Unmarshal(result, &out)
	if out.Error != "test error" {
		t.Errorf("expected 'test error', got %s", out.Error)
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
