package interpreter

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/hashicorp/go-hclog"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
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

// encodeCommand encodes a command string to UTF-16LE then base64, matching the wire format
func encodeCommand(command string) string {
	encoder := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewEncoder()
	utf16, _, _ := transform.String(encoder, command)
	return base64.StdEncoding.EncodeToString([]byte(utf16))
}

func TestMessage_Parse(t *testing.T) {
	data := []byte(`{"post_id":"abc:123","commands":"dGVzdA==","get_installation":false}`)
	var msg Message
	err := msg.Parse(data)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if msg.PostId != "abc:123" {
		t.Errorf("expected post_id 'abc:123', got %s", msg.PostId)
	}

	if msg.Commands != "dGVzdA==" {
		t.Errorf("expected commands 'dGVzdA==', got %s", msg.Commands)
	}

	if msg.GetInstallation {
		t.Error("expected get_installation false")
	}
}

func TestMessage_Parse_Invalid(t *testing.T) {
	var msg Message
	err := msg.Parse([]byte("not json"))

	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestMessage_Parse_GetInstallation(t *testing.T) {
	data := []byte(`{"post_id":"abc:123","get_installation":true}`)
	var msg Message
	err := msg.Parse(data)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !msg.GetInstallation {
		t.Error("expected get_installation true")
	}
}

func TestMessage_Execute_Noop(t *testing.T) {
	logger := hclog.NewNullLogger()
	msg := Message{}
	device := agent.Device{RewstOrgId: "test-org"}

	result := msg.Execute(nil, context.Background(), device, logger, nil, nil)

	var out errorResult
	json.Unmarshal(result, &out)
	if out.Error != "noop" {
		t.Errorf("expected 'noop', got %s", out.Error)
	}
}

func TestMessage_Execute_InvalidBase64(t *testing.T) {
	logger := hclog.NewNullLogger()
	msg := Message{
		Commands: "not-valid-base64!!!",
	}
	device := agent.Device{RewstOrgId: "test-org"}
	executor := NewExecutor()

	resultBytes := msg.Execute(executor, context.Background(), device, logger, nil, nil)

	var out errorResult
	json.Unmarshal(resultBytes, &out)
	if out.Error == "" {
		t.Error("expected error for invalid base64")
	}
}

type mockSystemInfoProvider struct{}

func (m *mockSystemInfoProvider) Hostname() (string, error)         { return "test-host", nil }
func (m *mockSystemInfoProvider) HostPlatform() (string, error)     { return "test-os", nil }
func (m *mockSystemInfoProvider) CPUModelName() (string, error)     { return "test-cpu", nil }
func (m *mockSystemInfoProvider) TotalMemoryBytes() (uint64, error) { return 1024 * 1024 * 1024, nil }
func (m *mockSystemInfoProvider) MACAddress() (*string, error)      { return nil, nil }

type mockDomainInfoProvider struct{}

func (m *mockDomainInfoProvider) ADDomain(context.Context) (*string, error) { return nil, nil }
func (m *mockDomainInfoProvider) IsADDomainController(context.Context) (bool, error) {
	return false, nil
}
func (m *mockDomainInfoProvider) IsEntraConnectServer() (bool, error)          { return false, nil }
func (m *mockDomainInfoProvider) EntraDomain(context.Context) (*string, error) { return nil, nil }

func TestMessage_Execute_GetInstallation(t *testing.T) {
	logger := hclog.NewNullLogger()
	msg := Message{
		GetInstallation: true,
	}
	device := agent.Device{RewstOrgId: "test-org"}
	sys := &mockSystemInfoProvider{}
	domain := &mockDomainInfoProvider{}

	resultBytes := msg.Execute(nil, context.Background(), device, logger, sys, domain)

	var out agent.PathsData
	err := json.Unmarshal(resultBytes, &out)
	if err != nil {
		t.Fatalf("expected valid JSON, got %v\nraw: %s", err, string(resultBytes))
	}

	if out.Tags == nil {
		t.Fatal("expected Tags to be non-nil")
	}

	if out.Tags.OrgId != "test-org" {
		t.Errorf("expected org_id 'test-org', got %s", out.Tags.OrgId)
	}

	if out.Tags.HostName != "test-host" {
		t.Errorf("expected hostname 'test-host', got %s", out.Tags.HostName)
	}
}

func TestMessage_Execute_DebugLogger_WritesEntries(t *testing.T) {
	var buf bytes.Buffer
	logger := hclog.New(&hclog.LoggerOptions{
		Output: &buf,
		Level:  hclog.Debug,
	})
	msg := Message{
		PostId:   "test:123",
		Commands: encodeCommand("echo 'hello world'"),
	}
	device := agent.Device{RewstOrgId: "test-org"}
	executor := NewExecutor()
	sys := &mockSystemInfoProvider{}
	domain := &mockDomainInfoProvider{}

	msg.Execute(executor, context.Background(), device, logger, sys, domain)

	if buf.Len() == 0 {
		t.Error("expected log entries to be written, but buffer is empty")
	}

	logs := buf.String()

	if !strings.Contains(strings.ToLower(logs), "[debug] shell version") {
		t.Errorf("expected log entries to write shell version, but not found in %s", logs)
	}

	if !strings.Contains(strings.ToLower(logs), "[debug] whoami") {
		t.Errorf("expected log entries to write whoami, but not found in %s", logs)
	}
}

func TestMessage_CreatePostbackRequest_MultipleColons(t *testing.T) {
	msg := Message{
		PostId: "a:b:c",
	}
	device := agent.Device{
		RewstEngineHost: "example.com",
	}
	req, err := msg.CreatePostbackRequest(context.Background(), device, nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedUrl := "https://example.com/webhooks/custom/action/a/b/c"
	if req.URL.String() != expectedUrl {
		t.Errorf("expected URL %s, got %s", expectedUrl, req.URL.String())
	}
}
