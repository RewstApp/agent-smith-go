package plugins

import (
	"bytes"
	"errors"
	"testing"

	"github.com/RewstApp/agent-smith-go/internal/agent"
)

// mockNotifier for testing
type mockNotifier struct {
	notifyErr error
	messages  []string
}

func (m *mockNotifier) Notify(message string) error {
	m.messages = append(m.messages, message)
	return m.notifyErr
}

// TestNotifierWrapper_InterfaceCompliance verifies NotifierWrapper interface
func TestNotifierWrapper_InterfaceCompliance(t *testing.T) {
	var _ NotifierWrapper = (*optionalNotifierWrapper)(nil)
	var _ NotifierWrapper = (*notifierSetWrapper)(nil)
}

// TestOptionalNotifierWrapper_Kill_WithNilClient tests Kill with nil client
func TestOptionalNotifierWrapper_Kill_WithNilClient(t *testing.T) {
	wrapper := &optionalNotifierWrapper{
		client: nil,
		plugin: nil,
		name:   "test",
	}

	// Should not panic
	wrapper.Kill()
}

// TestOptionalNotifierWrapper_Plugins tests Plugins method
func TestOptionalNotifierWrapper_Plugins(t *testing.T) {
	tests := []struct {
		name         string
		pluginName   string
		expectedName string
	}{
		{"simple_name", "test-plugin", "test-plugin"},
		{"with_spaces", "my plugin", "my plugin"},
		{"empty_name", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wrapper := &optionalNotifierWrapper{
				name: tt.pluginName,
			}

			plugins := wrapper.Plugins()
			if len(plugins) != 1 {
				t.Fatalf("expected 1 plugin, got %d", len(plugins))
			}

			if plugins[0] != tt.expectedName {
				t.Errorf("expected plugin name %q, got %q", tt.expectedName, plugins[0])
			}
		})
	}
}

// TestOptionalNotifierWrapper_Notify_WithNilPlugin tests Notify with nil plugin
func TestOptionalNotifierWrapper_Notify_WithNilPlugin(t *testing.T) {
	wrapper := &optionalNotifierWrapper{
		client: nil,
		plugin: nil,
		name:   "test",
	}

	err := wrapper.Notify("test message")
	if err != nil {
		t.Errorf("expected nil error when plugin is nil, got %v", err)
	}
}

// TestOptionalNotifierWrapper_Notify_Success tests successful notification
func TestOptionalNotifierWrapper_Notify_Success(t *testing.T) {
	mock := &mockNotifier{}
	wrapper := &optionalNotifierWrapper{
		plugin: mock,
		name:   "test",
	}

	err := wrapper.Notify("hello world")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(mock.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mock.messages))
	}

	if mock.messages[0] != "hello world" {
		t.Errorf("expected message 'hello world', got %q", mock.messages[0])
	}
}

// TestOptionalNotifierWrapper_Notify_Error tests notification with error
func TestOptionalNotifierWrapper_Notify_Error(t *testing.T) {
	expectedErr := errors.New("notify failed")
	mock := &mockNotifier{notifyErr: expectedErr}
	wrapper := &optionalNotifierWrapper{
		plugin: mock,
		name:   "test",
	}

	err := wrapper.Notify("test")
	if err != expectedErr {
		t.Errorf("expected error %v, got %v", expectedErr, err)
	}
}

// TestNotifierSetWrapper_Kill_Empty tests Kill with empty set
func TestNotifierSetWrapper_Kill_Empty(t *testing.T) {
	set := &notifierSetWrapper{
		notifiers: []*optionalNotifierWrapper{},
	}

	// Should not panic
	set.Kill()
}

// TestNotifierSetWrapper_Kill_Multiple tests Kill with multiple notifiers
func TestNotifierSetWrapper_Kill_Multiple(t *testing.T) {
	set := &notifierSetWrapper{
		notifiers: []*optionalNotifierWrapper{
			{client: nil, name: "plugin1"},
			{client: nil, name: "plugin2"},
			{client: nil, name: "plugin3"},
		},
	}

	// Should not panic and should call Kill on all
	set.Kill()
}

// TestNotifierSetWrapper_Plugins_Empty tests Plugins with empty set
func TestNotifierSetWrapper_Plugins_Empty(t *testing.T) {
	set := &notifierSetWrapper{
		notifiers: []*optionalNotifierWrapper{},
	}

	plugins := set.Plugins()
	if len(plugins) != 0 {
		t.Errorf("expected 0 plugins, got %d", len(plugins))
	}
}

// TestNotifierSetWrapper_Plugins_Multiple tests Plugins with multiple notifiers
func TestNotifierSetWrapper_Plugins_Multiple(t *testing.T) {
	set := &notifierSetWrapper{
		notifiers: []*optionalNotifierWrapper{
			{name: "plugin1"},
			{name: "plugin2"},
			{name: "plugin3"},
		},
	}

	plugins := set.Plugins()
	if len(plugins) != 3 {
		t.Fatalf("expected 3 plugins, got %d", len(plugins))
	}

	expectedNames := []string{"plugin1", "plugin2", "plugin3"}
	for i, expected := range expectedNames {
		if plugins[i] != expected {
			t.Errorf("expected plugin[%d] to be %q, got %q", i, expected, plugins[i])
		}
	}
}

// TestNotifierSetWrapper_Notify_Empty tests Notify with empty set
func TestNotifierSetWrapper_Notify_Empty(t *testing.T) {
	set := &notifierSetWrapper{
		notifiers: []*optionalNotifierWrapper{},
	}

	err := set.Notify("test message")
	if err != nil {
		t.Errorf("expected nil error for empty set, got %v", err)
	}
}

// TestNotifierSetWrapper_Notify_Single tests Notify with single notifier
func TestNotifierSetWrapper_Notify_Single(t *testing.T) {
	mock := &mockNotifier{}
	set := &notifierSetWrapper{
		notifiers: []*optionalNotifierWrapper{
			{plugin: mock, name: "plugin1"},
		},
	}

	err := set.Notify("test message")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if len(mock.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mock.messages))
	}

	if mock.messages[0] != "test message" {
		t.Errorf("expected 'test message', got %q", mock.messages[0])
	}
}

// TestNotifierSetWrapper_Notify_Multiple tests Notify with multiple notifiers
func TestNotifierSetWrapper_Notify_Multiple(t *testing.T) {
	mock1 := &mockNotifier{}
	mock2 := &mockNotifier{}
	mock3 := &mockNotifier{}

	set := &notifierSetWrapper{
		notifiers: []*optionalNotifierWrapper{
			{plugin: mock1, name: "plugin1"},
			{plugin: mock2, name: "plugin2"},
			{plugin: mock3, name: "plugin3"},
		},
	}

	err := set.Notify("broadcast message")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify all mocks received the message
	mocks := []*mockNotifier{mock1, mock2, mock3}
	for i, mock := range mocks {
		if len(mock.messages) != 1 {
			t.Errorf("mock[%d]: expected 1 message, got %d", i, len(mock.messages))
		}
		if len(mock.messages) > 0 && mock.messages[0] != "broadcast message" {
			t.Errorf("mock[%d]: expected 'broadcast message', got %q", i, mock.messages[0])
		}
	}
}

// TestNotifierSetWrapper_Notify_WithErrors tests Notify with some errors
func TestNotifierSetWrapper_Notify_WithErrors(t *testing.T) {
	err1 := errors.New("error1")
	err2 := errors.New("error2")

	mock1 := &mockNotifier{notifyErr: err1}
	mock2 := &mockNotifier{} // no error
	mock3 := &mockNotifier{notifyErr: err2}

	set := &notifierSetWrapper{
		notifiers: []*optionalNotifierWrapper{
			{plugin: mock1, name: "plugin1"},
			{plugin: mock2, name: "plugin2"},
			{plugin: mock3, name: "plugin3"},
		},
	}

	err := set.Notify("test")
	if err == nil {
		t.Fatal("expected combined error, got nil")
	}

	// Verify error contains both errors
	errStr := err.Error()
	if !contains(errStr, "error1") {
		t.Errorf("expected combined error to contain 'error1', got %q", errStr)
	}
	if !contains(errStr, "error2") {
		t.Errorf("expected combined error to contain 'error2', got %q", errStr)
	}

	// Verify all plugins were called despite errors
	if len(mock1.messages) != 1 {
		t.Error("expected plugin1 to be called")
	}
	if len(mock2.messages) != 1 {
		t.Error("expected plugin2 to be called")
	}
	if len(mock3.messages) != 1 {
		t.Error("expected plugin3 to be called")
	}
}

// TestNotifierSetWrapper_Notify_WithNilPlugins tests Notify with nil plugins in the set
func TestNotifierSetWrapper_Notify_WithNilPlugins(t *testing.T) {
	mock := &mockNotifier{}

	set := &notifierSetWrapper{
		notifiers: []*optionalNotifierWrapper{
			{plugin: nil, name: "nil-plugin"},
			{plugin: mock, name: "real-plugin"},
		},
	}

	err := set.Notify("test")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Only the non-nil plugin should receive the message
	if len(mock.messages) != 1 {
		t.Errorf("expected real plugin to receive message")
	}
}

// TestLoadNotifer_EmptyPluginList tests loading with empty plugin list
func TestLoadNotifer_EmptyPluginList(t *testing.T) {
	logBuf := &bytes.Buffer{}
	plugins := []agent.Plugin{}

	wrapper, err := LoadNotifer(plugins, logBuf)
	if err != nil {
		t.Errorf("expected no error for empty list, got %v", err)
	}

	if wrapper == nil {
		t.Fatal("expected non-nil wrapper")
	}

	pluginNames := wrapper.Plugins()
	if len(pluginNames) != 0 {
		t.Errorf("expected 0 plugins, got %d", len(pluginNames))
	}
}

// TestLoadNotifer_InvalidExecutable tests loading with non-existent executable
func TestLoadNotifer_InvalidExecutable(t *testing.T) {
	logBuf := &bytes.Buffer{}
	plugins := []agent.Plugin{
		{
			Name:           "test-plugin",
			ExecutablePath: "/nonexistent/path/to/plugin",
		},
	}

	wrapper, err := LoadNotifer(plugins, logBuf)

	// Should return a wrapper even with errors
	if wrapper == nil {
		t.Fatal("expected non-nil wrapper even with errors")
	}

	// Should return an error since the plugin couldn't be loaded
	if err == nil {
		t.Error("expected error for invalid executable, got nil")
	}

	// The wrapper should have 0 successfully loaded plugins
	pluginNames := wrapper.Plugins()
	if len(pluginNames) != 0 {
		t.Errorf("expected 0 successfully loaded plugins, got %d", len(pluginNames))
	}
}

// TestLoadNotifer_MultipleInvalidPlugins tests loading multiple invalid plugins
func TestLoadNotifer_MultipleInvalidPlugins(t *testing.T) {
	logBuf := &bytes.Buffer{}
	plugins := []agent.Plugin{
		{
			Name:           "plugin1",
			ExecutablePath: "/invalid/path1",
		},
		{
			Name:           "plugin2",
			ExecutablePath: "/invalid/path2",
		},
		{
			Name:           "plugin3",
			ExecutablePath: "/invalid/path3",
		},
	}

	wrapper, err := LoadNotifer(plugins, logBuf)

	if wrapper == nil {
		t.Fatal("expected non-nil wrapper")
	}

	// Should have combined errors
	if err == nil {
		t.Error("expected combined error for multiple invalid plugins")
	}

	// No plugins should be loaded
	pluginNames := wrapper.Plugins()
	if len(pluginNames) != 0 {
		t.Errorf("expected 0 plugins, got %d", len(pluginNames))
	}
}

// TestLoadNotifer_NilLogWriter tests loading with nil log writer
func TestLoadNotifer_NilLogWriter(t *testing.T) {
	plugins := []agent.Plugin{
		{
			Name:           "test",
			ExecutablePath: "/invalid/path",
		},
	}

	wrapper, err := LoadNotifer(plugins, nil)

	if wrapper == nil {
		t.Fatal("expected non-nil wrapper")
	}

	// Should still attempt to load (and fail) with nil writer
	if err == nil {
		t.Log("Got nil error (acceptable behavior)")
	}
}

// TestPluginMapExists tests that pluginMap is properly defined
func TestPluginMapExists(t *testing.T) {
	if pluginMap == nil {
		t.Fatal("pluginMap should not be nil")
	}

	if _, ok := pluginMap["notifier"]; !ok {
		t.Error("pluginMap should contain 'notifier' key")
	}
}

// TestDefaultConstants tests the default constants
func TestDefaultConstants(t *testing.T) {
	if defaultProtocolVersion != 1 {
		t.Errorf("expected defaultProtocolVersion to be 1, got %d", defaultProtocolVersion)
	}

	if defaultMagicCookieKey != "AGENT_SMITH" {
		t.Errorf(
			"expected defaultMagicCookieKey to be 'AGENT_SMITH', got %q",
			defaultMagicCookieKey,
		)
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr || len(s) > len(substr) && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
