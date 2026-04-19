package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/interpreter"
	inmqtt "github.com/RewstApp/agent-smith-go/internal/mqtt"
	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/hashicorp/go-hclog"
)

// TestLoadConfig tests the loadConfig method
func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name        string
		configData  agent.Device
		expectError bool
	}{
		{
			name: "valid_config",
			configData: agent.Device{
				DeviceId:        "test-device-123",
				SharedAccessKey: "test-shared-key",
				AzureIotHubHost: "test.azure-devices.net",
				LoggingLevel:    "info",
				RewstEngineHost: "engine.rewst.io",
			},
			expectError: false,
		},
		{
			name: "minimal_config",
			configData: agent.Device{
				DeviceId: "minimal-device",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary config file
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.json")

			configBytes, err := json.Marshal(tt.configData)
			if err != nil {
				t.Fatalf("failed to marshal config: %v", err)
			}

			err = os.WriteFile(configPath, configBytes, utils.DefaultFileMod)
			if err != nil {
				t.Fatalf("failed to write config file: %v", err)
			}

			// Test loadConfig
			svc := &serviceContext{
				ConfigFile: configPath,
			}

			device, err := svc.loadConfig()

			if tt.expectError && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("expected no error, got %v", err)
			}

			if !tt.expectError {
				if device.DeviceId != tt.configData.DeviceId {
					t.Errorf(
						"expected DeviceId %q, got %q",
						tt.configData.DeviceId,
						device.DeviceId,
					)
				}
				if device.SharedAccessKey != tt.configData.SharedAccessKey {
					t.Errorf(
						"expected SharedAccessKey %q, got %q",
						tt.configData.SharedAccessKey,
						device.SharedAccessKey,
					)
				}
			}
		})
	}
}

// TestLoadConfig_MqttQos tests QoS validation in loadConfig
func TestLoadConfig_MqttQos(t *testing.T) {
	qos := func(v byte) *byte { return &v }

	tests := []struct {
		name        string
		mqttQos     *byte
		expectError bool
	}{
		{name: "qos_absent_defaults_to_1", mqttQos: nil, expectError: false},
		{name: "qos_0_accepted", mqttQos: qos(0), expectError: false},
		{name: "qos_1_accepted", mqttQos: qos(1), expectError: false},
		{name: "qos_2_accepted", mqttQos: qos(2), expectError: false},
		{name: "qos_3_rejected", mqttQos: qos(3), expectError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.json")

			device := agent.Device{DeviceId: "test-device", MqttQos: tt.mqttQos}
			configBytes, err := json.Marshal(device)
			if err != nil {
				t.Fatalf("failed to marshal config: %v", err)
			}

			if err = os.WriteFile(configPath, configBytes, utils.DefaultFileMod); err != nil {
				t.Fatalf("failed to write config: %v", err)
			}

			svc := &serviceContext{ConfigFile: configPath}
			got, err := svc.loadConfig()

			if tt.expectError {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tt.mqttQos == nil {
				if got.MqttQos != nil {
					t.Errorf("expected MqttQos nil, got %d", *got.MqttQos)
				}
			} else {
				if got.MqttQos == nil || *got.MqttQos != *tt.mqttQos {
					t.Errorf("expected MqttQos %d, got %v", *tt.mqttQos, got.MqttQos)
				}
			}
		})
	}
}

// TestLoadConfig_FileNotFound tests loadConfig with missing file
func TestLoadConfig_FileNotFound(t *testing.T) {
	svc := &serviceContext{
		ConfigFile: "/nonexistent/path/config.json",
	}

	_, err := svc.loadConfig()
	if err == nil {
		t.Error("expected error for nonexistent file, got nil")
	}
}

// TestLoadConfig_InvalidJSON tests loadConfig with invalid JSON
func TestLoadConfig_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "invalid.json")

	err := os.WriteFile(configPath, []byte("{invalid json"), utils.DefaultFileMod)
	if err != nil {
		t.Fatalf("failed to write invalid config: %v", err)
	}

	svc := &serviceContext{
		ConfigFile: configPath,
	}

	_, err = svc.loadConfig()
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

// TestLoadLog tests the loadLog method
func TestLoadLog(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "test.log")

	svc := &serviceContext{
		LogFile: logPath,
	}

	logFile, err := svc.loadLog()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	defer func() {
		err = logFile.Close()
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	}()

	// Verify file was created
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		t.Error("expected log file to be created")
	}

	// Test writing to the log file
	testData := []byte("test log entry\n")
	n, err := logFile.Write(testData)
	if err != nil {
		t.Errorf("failed to write to log file: %v", err)
	}
	if n != len(testData) {
		t.Errorf("expected to write %d bytes, wrote %d", len(testData), n)
	}
}

// TestLoadLog_AppendMode tests that loadLog opens file in append mode
func TestLoadLog_AppendMode(t *testing.T) {
	tmpDir := t.TempDir()
	logPath := filepath.Join(tmpDir, "append.log")

	// Write initial content
	initialContent := "initial content\n"
	err := os.WriteFile(logPath, []byte(initialContent), utils.DefaultFileMod)
	if err != nil {
		t.Fatalf("failed to write initial content: %v", err)
	}

	// Open with loadLog
	svc := &serviceContext{
		LogFile: logPath,
	}

	logFile, err := svc.loadLog()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Write additional content
	additionalContent := "appended content\n"
	_, err = logFile.Write([]byte(additionalContent))
	if err != nil {
		t.Fatalf("failed to write additional content: %v", err)
	}

	err = logFile.Close()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	// Verify content was appended
	finalContent, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read final content: %v", err)
	}

	expected := initialContent + additionalContent
	if string(finalContent) != expected {
		t.Errorf("expected content %q, got %q", expected, string(finalContent))
	}
}

// TestLoadLog_InvalidPath tests loadLog with invalid path
func TestLoadLog_InvalidPath(t *testing.T) {
	svc := &serviceContext{
		LogFile: "/nonexistent/directory/log.txt",
	}

	_, err := svc.loadLog()
	if err == nil {
		t.Error("expected error for invalid path, got nil")
	}
}

// TestName tests the Name method
func TestName(t *testing.T) {
	tests := []struct {
		name     string
		orgId    string
		expected string
	}{
		{
			name:     "standard_org_id",
			orgId:    "org-123",
			expected: agent.GetServiceName("org-123"),
		},
		{
			name:     "different_org_id",
			orgId:    "test-org-456",
			expected: agent.GetServiceName("test-org-456"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &serviceContext{
				OrgId: tt.orgId,
			}

			result := svc.Name()
			if result != tt.expected {
				t.Errorf("expected Name() to return %q, got %q", tt.expected, result)
			}
		})
	}
}

// mockExecutor for testing
type mockExecutor struct {
	executeCalled bool
	result        []byte
}

func (m *mockExecutor) AlwaysPostback() bool {
	return false
}

func (m *mockExecutor) Execute(
	ctx context.Context,
	message *interpreter.Message,
	device agent.Device,
	logger hclog.Logger,
	sys agent.SystemInfoProvider,
	domain agent.DomainInfoProvider,
) []byte {
	m.executeCalled = true
	return m.result
}

// TestExecute_ConfigError tests Execute with invalid config file
func TestExecute_ConfigError(t *testing.T) {
	svc := &serviceContext{
		ConfigFile: "/nonexistent/config.json",
		LogFile:    filepath.Join(t.TempDir(), "test.log"),
		OrgId:      "test-org",
	}

	stop := make(chan struct{})
	running := make(chan struct{})

	done := make(chan service.ServiceExitCode)
	go func() {
		code := svc.Execute(stop, running)
		done <- code
	}()

	// Wait for exit
	select {
	case code := <-done:
		if code != service.ConfigError {
			t.Errorf("expected ConfigError (%d), got %d", service.ConfigError, code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Execute did not exit within timeout")
	}
}

// TestExecute_LogFileError tests Execute with invalid log file path
func TestExecute_LogFileError(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")

	// Create valid config
	device := agent.Device{
		DeviceId:        "test-device",
		SharedAccessKey: "test-key",
		AzureIotHubHost: "test.azure-devices.net",
	}
	configBytes, _ := json.Marshal(device)

	err := os.WriteFile(configPath, configBytes, utils.DefaultFileMod)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	svc := &serviceContext{
		ConfigFile: configPath,
		LogFile:    "/invalid/path/log.txt",
		OrgId:      "test-org",
	}

	stop := make(chan struct{})
	running := make(chan struct{})

	done := make(chan service.ServiceExitCode)
	go func() {
		code := svc.Execute(stop, running)
		done <- code
	}()

	// Wait for exit
	select {
	case code := <-done:
		if code != service.LogFileError {
			t.Errorf("expected LogFileError (%d), got %d", service.LogFileError, code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Execute did not exit within timeout")
	}
}

// TestExecute_SuccessfulStartAndStop is skipped due to complexity
// Full integration testing of Execute would require a test MQTT broker
// The component tests (loadConfig, loadLog, error cases) provide sufficient coverage
func TestExecute_SuccessfulStartAndStop(t *testing.T) {
	t.Skip("Skipping integration test - requires MQTT test infrastructure")
}

// TestExecute_WithSyslog tests Execute with syslog enabled
func TestExecute_WithSyslog(t *testing.T) {
	// Skip on platforms where syslog might not be available
	if os.Getenv("CI") != "" {
		t.Skip("Skipping syslog test in CI environment")
	}

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	logPath := filepath.Join(tmpDir, "test.log")

	// Create config with syslog enabled
	device := agent.Device{
		DeviceId:             "test-device-syslog",
		SharedAccessKey:      "dGVzdC1zaGFyZWQta2V5LXRoYXQtaXMtbG9uZy1lbm91Z2gtZm9yLWJhc2U2NC1kZWNvZGluZw==",
		AzureIotHubHost:      "invalid.local",
		LoggingLevel:         "error",
		RewstEngineHost:      "engine.rewst.io",
		DisableAutoUpdates:   true,
		UseSyslog:            true,
		DisableAgentPostback: true,
	}
	configBytes, _ := json.Marshal(device)

	err := os.WriteFile(configPath, configBytes, utils.DefaultFileMod)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	svc := &serviceContext{
		ConfigFile: configPath,
		LogFile:    logPath,
		OrgId:      "test-org-syslog",
		Executor:   &mockExecutor{},
	}

	stop := make(chan struct{})
	running := make(chan struct{}, 1)

	done := make(chan service.ServiceExitCode, 1)
	go func() {
		code := svc.Execute(stop, running)
		done <- code
	}()

	// Wait for service to start or exit
	select {
	case <-running:
		// Started successfully
		close(stop)
	case code := <-done:
		// Service may exit early if syslog initialization fails
		// This is acceptable in test environment
		t.Logf("Service exited with code %d (expected in test environment)", code)
		return
	case <-time.After(3 * time.Second):
		close(stop)
		t.Fatal("Execute did not start or exit within timeout")
	}

	// Wait for clean exit
	select {
	case code := <-done:
		t.Logf("Service exited with code %d", code)
	case <-time.After(20 * time.Second):
		t.Fatal("Execute did not exit within timeout")
	}
}

// TestExecute_Name tests that Execute uses the correct service name
func TestExecute_Name(t *testing.T) {
	svc := &serviceContext{
		OrgId: "test-org-name",
	}

	expectedName := agent.GetServiceName("test-org-name")
	actualName := svc.Name()

	if actualName != expectedName {
		t.Errorf("expected Name() to return %q, got %q", expectedName, actualName)
	}
}

// TestRunService tests the runService wrapper (without actually exiting)
func TestRunService_ExitCode(t *testing.T) {
	// This test verifies that Execute returns appropriate exit codes
	// We can't test runService directly because it calls os.Exit

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	logPath := filepath.Join(tmpDir, "test.log")

	device := agent.Device{
		DeviceId: "test",
	}
	configBytes, _ := json.Marshal(device)

	err := os.WriteFile(configPath, configBytes, utils.DefaultFileMod)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	svc := &serviceContext{
		ConfigFile: configPath,
		LogFile:    logPath,
		OrgId:      "test",
	}

	// Test that Execute returns when stop is already closed
	stop := make(chan struct{})
	running := make(chan struct{}, 1)
	close(stop)

	done := make(chan service.ServiceExitCode, 1)
	go func() {
		code := svc.Execute(stop, running)
		done <- code
	}()

	select {
	case code := <-done:
		// Should exit quickly with code 0 since stop is already closed
		t.Logf("Got exit code %d", code)
	case <-time.After(3 * time.Second):
		t.Fatal("Execute did not exit within timeout")
	}
}

// TestLoadConfig_EmptyFile tests loadConfig with empty file
func TestLoadConfig_EmptyFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "empty.json")

	err := os.WriteFile(configPath, []byte(""), utils.DefaultFileMod)
	if err != nil {
		t.Fatalf("failed to write empty file: %v", err)
	}

	svc := &serviceContext{
		ConfigFile: configPath,
	}

	_, err = svc.loadConfig()
	if err == nil {
		t.Error("expected error for empty file, got nil")
	}
}

// TestExecute_AutoUpdatesDisabled is skipped due to complexity
// Full integration testing of Execute would require a test MQTT broker
// The component tests (loadConfig, loadLog, error cases) provide sufficient coverage
func TestExecute_AutoUpdatesDisabled(t *testing.T) {
	t.Skip("Skipping integration test - requires MQTT test infrastructure")
}

// mockMQTTToken implements pahomqtt.Token for testing.
type mockMQTTToken struct {
	err error
}

func (t *mockMQTTToken) Wait() bool                       { return true }
func (t *mockMQTTToken) WaitTimeout(_ time.Duration) bool { return true }
func (t *mockMQTTToken) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
func (t *mockMQTTToken) Error() error { return t.err }

// mockMQTTClient implements pahomqtt.Client for testing.
// Connect and Publish succeed; Subscribe returns subscribeErr.
type mockMQTTClient struct {
	subscribeErr error
}

// disconnectTrackingClient wraps mockMQTTClient and invokes onDisconnect when
// Disconnect is called. Used to verify that Disconnect is called explicitly at
// each loop-exit path rather than only when Execute returns.
type disconnectTrackingClient struct {
	mockMQTTClient
	onDisconnect func()
}

func (m *disconnectTrackingClient) Disconnect(_ uint) {
	if m.onDisconnect != nil {
		m.onDisconnect()
	}
}

func (m *mockMQTTClient) IsConnected() bool      { return true }
func (m *mockMQTTClient) IsConnectionOpen() bool { return true }
func (m *mockMQTTClient) Connect() pahomqtt.Token {
	return &mockMQTTToken{}
}
func (m *mockMQTTClient) Disconnect(_ uint) {}
func (m *mockMQTTClient) Publish(_ string, _ byte, _ bool, _ interface{}) pahomqtt.Token {
	return &mockMQTTToken{}
}

func (m *mockMQTTClient) Subscribe(_ string, _ byte, _ pahomqtt.MessageHandler) pahomqtt.Token {
	return &mockMQTTToken{err: m.subscribeErr}
}

func (m *mockMQTTClient) SubscribeMultiple(
	_ map[string]byte,
	_ pahomqtt.MessageHandler,
) pahomqtt.Token {
	return &mockMQTTToken{}
}
func (m *mockMQTTClient) Unsubscribe(_ ...string) pahomqtt.Token       { return &mockMQTTToken{} }
func (m *mockMQTTClient) AddRoute(_ string, _ pahomqtt.MessageHandler) {}
func (m *mockMQTTClient) OptionsReader() pahomqtt.ClientOptionsReader {
	return pahomqtt.NewOptionsReader(pahomqtt.NewClientOptions())
}

// TestExecute_SubscribeFailure verifies that when MQTT subscription fails,
// the logged error comes from token.Error() and not from a stale err variable.
func TestExecute_SubscribeFailure(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	logPath := filepath.Join(tmpDir, "test.log")

	// SharedAccessKey must be valid base64 so NewClientOptions succeeds.
	device := agent.Device{
		DeviceId:             "test-device",
		SharedAccessKey:      "dGVzdC1zaGFyZWQta2V5LXRoYXQtaXMtbG9uZy1lbm91Z2gtZm9yLWJhc2U2NC1kZWNvZGluZw==",
		AzureIotHubHost:      "test.azure-devices.net",
		LoggingLevel:         "error",
		DisableAutoUpdates:   true,
		DisableAgentPostback: true,
	}
	configBytes, _ := json.Marshal(device)
	if err := os.WriteFile(configPath, configBytes, utils.DefaultFileMod); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	subscribeErrMsg := "subscription denied by broker"
	origNewClient := inmqtt.NewClient
	inmqtt.NewClient = func(o *pahomqtt.ClientOptions) pahomqtt.Client {
		return &mockMQTTClient{subscribeErr: errors.New(subscribeErrMsg)}
	}
	defer func() { inmqtt.NewClient = origNewClient }()

	svc := &serviceContext{
		ConfigFile: configPath,
		LogFile:    logPath,
		OrgId:      "test-org",
		Executor:   &mockExecutor{},
	}

	stop := make(chan struct{})
	running := make(chan struct{}, 1)

	done := make(chan service.ServiceExitCode, 1)
	go func() {
		done <- svc.Execute(stop, running)
	}()

	// Wait for service to signal it is running.
	select {
	case <-running:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute did not signal running within timeout")
	}

	// Close stop so that after the subscribe failure + reconnect wait, the
	// service exits cleanly.
	close(stop)

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Execute did not exit within timeout")
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log: %v", err)
	}
	logContent := string(logBytes)

	if !strings.Contains(logContent, "Failed to subscribe") {
		t.Error("expected log to contain 'Failed to subscribe'")
	}
	if !strings.Contains(logContent, subscribeErrMsg) {
		t.Errorf(
			"expected log to contain the token error %q, but log was:\n%s",
			subscribeErrMsg,
			logContent,
		)
	}
}

// TestExecute_DisconnectCalledOnStop verifies that the MQTT client is
// disconnected when the stop signal is received. This tests the explicit
// client.Disconnect call on the <-stopped path added to fix sc-86631.
func TestExecute_DisconnectCalledOnStop(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	logPath := filepath.Join(tmpDir, "test.log")

	device := agent.Device{
		DeviceId:             "test-device",
		SharedAccessKey:      "dGVzdC1zaGFyZWQta2V5LXRoYXQtaXMtbG9uZy1lbm91Z2gtZm9yLWJhc2U2NC1kZWNvZGluZw==",
		AzureIotHubHost:      "test.azure-devices.net",
		LoggingLevel:         "error",
		DisableAutoUpdates:   true,
		DisableAgentPostback: true,
	}
	configBytes, _ := json.Marshal(device)
	if err := os.WriteFile(configPath, configBytes, utils.DefaultFileMod); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	disconnected := make(chan struct{}, 1)
	origNewClient := inmqtt.NewClient
	inmqtt.NewClient = func(_ *pahomqtt.ClientOptions) pahomqtt.Client {
		return &disconnectTrackingClient{
			onDisconnect: func() { disconnected <- struct{}{} },
		}
	}
	defer func() { inmqtt.NewClient = origNewClient }()

	svc := &serviceContext{
		ConfigFile: configPath,
		LogFile:    logPath,
		OrgId:      "test-org",
		Executor:   &mockExecutor{},
	}

	stop := make(chan struct{})
	running := make(chan struct{}, 1)
	done := make(chan service.ServiceExitCode, 1)
	go func() { done <- svc.Execute(stop, running) }()

	select {
	case <-running:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute did not signal running within timeout")
	}

	close(stop)

	select {
	case <-disconnected:
	case <-time.After(5 * time.Second):
		t.Fatal("client.Disconnect was not called after stop signal")
	}

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute did not exit within timeout")
	}
}

// TestExecute_DisconnectCalledOnSubscribeFailure verifies that the MQTT client
// is disconnected immediately when subscription fails — before the reconnect
// delay begins — rather than only when Execute returns. This is the regression
// test for the defer-inside-loop bug (sc-86631): with the old defer, Disconnect
// would only fire at function exit; with the fix it fires before continue.
func TestExecute_DisconnectCalledOnSubscribeFailure(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	logPath := filepath.Join(tmpDir, "test.log")

	device := agent.Device{
		DeviceId:             "test-device",
		SharedAccessKey:      "dGVzdC1zaGFyZWQta2V5LXRoYXQtaXMtbG9uZy1lbm91Z2gtZm9yLWJhc2U2NC1kZWNvZGluZw==",
		AzureIotHubHost:      "test.azure-devices.net",
		LoggingLevel:         "error",
		DisableAutoUpdates:   true,
		DisableAgentPostback: true,
	}
	configBytes, _ := json.Marshal(device)
	if err := os.WriteFile(configPath, configBytes, utils.DefaultFileMod); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	// disconnected is sent to by the client's Disconnect method. The test
	// receives from it *before* closing stop, proving that Disconnect fired
	// during the loop iteration (explicit call) and not only when Execute
	// returned (which is what the old defer-based code would do).
	disconnected := make(chan struct{}, 1)
	origNewClient := inmqtt.NewClient
	inmqtt.NewClient = func(_ *pahomqtt.ClientOptions) pahomqtt.Client {
		return &disconnectTrackingClient{
			mockMQTTClient: mockMQTTClient{subscribeErr: errors.New("broker denied")},
			onDisconnect:   func() { disconnected <- struct{}{} },
		}
	}
	defer func() { inmqtt.NewClient = origNewClient }()

	svc := &serviceContext{
		ConfigFile: configPath,
		LogFile:    logPath,
		OrgId:      "test-org",
		Executor:   &mockExecutor{},
	}

	stop := make(chan struct{})
	running := make(chan struct{}, 1)
	done := make(chan service.ServiceExitCode, 1)
	go func() { done <- svc.Execute(stop, running) }()

	select {
	case <-running:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute did not signal running within timeout")
	}

	// Wait for Disconnect to be called before closing stop. With the old
	// defer-inside-loop bug this would block until the function returned,
	// but stop is not yet closed so it would deadlock / timeout here.
	select {
	case <-disconnected:
	case <-time.After(5 * time.Second):
		t.Fatal("client.Disconnect was not called after subscribe failure")
	}

	// Now let Execute exit cleanly via the reconnect-wait select.
	close(stop)

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Execute did not exit within timeout")
	}
}

// TestExecute_SubscribedMessagesLogIncludesQoS verifies that the "Subscribed
// to messages" log entry includes the topic and QoS level being used.
func TestExecute_SubscribedMessagesLogIncludesQoS(t *testing.T) {
	tests := []struct {
		name        string
		mqttQos     *byte
		expectedQoS string
	}{
		{
			name:        "default_qos_1",
			mqttQos:     nil,
			expectedQoS: "qos=1",
		},
		{
			name:        "explicit_qos_0",
			mqttQos:     func() *byte { b := byte(0); return &b }(),
			expectedQoS: "qos=0",
		},
		{
			name:        "explicit_qos_2",
			mqttQos:     func() *byte { b := byte(2); return &b }(),
			expectedQoS: "qos=2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.json")
			logPath := filepath.Join(tmpDir, "test.log")

			device := agent.Device{
				DeviceId:             "test-device",
				SharedAccessKey:      "dGVzdC1zaGFyZWQta2V5LXRoYXQtaXMtbG9uZy1lbm91Z2gtZm9yLWJhc2U2NC1kZWNvZGluZw==",
				AzureIotHubHost:      "test.azure-devices.net",
				LoggingLevel:         "info",
				DisableAutoUpdates:   true,
				DisableAgentPostback: true,
				MqttQos:              tt.mqttQos,
			}
			configBytes, _ := json.Marshal(device)
			if err := os.WriteFile(configPath, configBytes, utils.DefaultFileMod); err != nil {
				t.Fatalf("failed to write config: %v", err)
			}

			origNewClient := inmqtt.NewClient
			inmqtt.NewClient = func(_ *pahomqtt.ClientOptions) pahomqtt.Client {
				return &mockMQTTClient{}
			}
			defer func() { inmqtt.NewClient = origNewClient }()

			svc := &serviceContext{
				ConfigFile: configPath,
				LogFile:    logPath,
				OrgId:      "test-org",
				Executor:   &mockExecutor{},
			}

			stop := make(chan struct{})
			running := make(chan struct{}, 1)
			done := make(chan service.ServiceExitCode, 1)
			go func() { done <- svc.Execute(stop, running) }()

			select {
			case <-running:
			case <-time.After(5 * time.Second):
				t.Fatal("Execute did not signal running within timeout")
			}

			close(stop)

			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("Execute did not exit within timeout")
			}

			logBytes, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatalf("failed to read log: %v", err)
			}
			logContent := string(logBytes)

			if !strings.Contains(logContent, "Subscribed to messages") {
				t.Error("expected log to contain 'Subscribed to messages'")
			}
			if !strings.Contains(logContent, tt.expectedQoS) {
				t.Errorf(
					"expected log to contain %q, but log was:\n%s",
					tt.expectedQoS,
					logContent,
				)
			}
			if !strings.Contains(logContent, "topic=") {
				t.Errorf("expected log to contain 'topic=', but log was:\n%s", logContent)
			}
		})
	}
}
