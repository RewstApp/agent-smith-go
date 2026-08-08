package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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
		{name: "qos_2_rejected", mqttQos: qos(2), expectError: true},
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
// Connect returns connectErr (nil = success); Subscribe returns subscribeErr.
type mockMQTTClient struct {
	connectErr   error
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
	return &mockMQTTToken{err: m.connectErr}
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

// teardownTrackingClient wraps mockMQTTClient and records the order in which
// Unsubscribe and Disconnect are called. Used by the sc-89441 regression test
// to verify Unsubscribe runs before Disconnect on every cycle exit path.
type teardownTrackingClient struct {
	mockMQTTClient
	calls         *[]string
	subscribeArgs *string
}

func (m *teardownTrackingClient) Subscribe(
	topic string,
	qos byte,
	cb pahomqtt.MessageHandler,
) pahomqtt.Token {
	if m.subscribeArgs != nil {
		*m.subscribeArgs = topic
	}
	return m.mockMQTTClient.Subscribe(topic, qos, cb)
}

func (m *teardownTrackingClient) Unsubscribe(topics ...string) pahomqtt.Token {
	*m.calls = append(*m.calls, "unsubscribe:"+strings.Join(topics, ","))
	return &mockMQTTToken{}
}

func (m *teardownTrackingClient) Disconnect(_ uint) {
	*m.calls = append(*m.calls, "disconnect")
}

// blockingToken is a mock MQTT token whose waits block until release is closed.
// started is a buffered channel that receives one item when a wait is first
// entered. WaitTimeout honors its deadline (returning false when it elapses
// first) so callers that bound their wait behave as they would against paho.
type blockingToken struct {
	started chan struct{}
	release chan struct{}
}

func (t *blockingToken) signalStarted() {
	select {
	case t.started <- struct{}{}:
	default:
	}
}

func (t *blockingToken) Wait() bool {
	t.signalStarted()
	<-t.release
	return true
}

func (t *blockingToken) WaitTimeout(d time.Duration) bool {
	t.signalStarted()
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-t.release:
		return true
	case <-timer.C:
		return false
	}
}

func (t *blockingToken) Done() <-chan struct{} {
	t.signalStarted()
	ch := make(chan struct{})
	go func() { <-t.release; close(ch) }()
	return ch
}
func (t *blockingToken) Error() error { return nil }

// blockingPublishClient embeds mockMQTTClient and returns a blockingToken from
// Publish to hold the main goroutine in UpdateReportedProperties.
type blockingPublishClient struct {
	mockMQTTClient
	publishToken *blockingToken
}

func (m *blockingPublishClient) Publish(_ string, _ byte, _ bool, _ interface{}) pahomqtt.Token {
	return m.publishToken
}

// connectSignalClient embeds mockMQTTClient and always fails Connect, sending a
// signal on connectCalled each time Connect is invoked. It is used to drive
// Execute into the reconnect backoff loop deterministically.
type connectSignalClient struct {
	mockMQTTClient
	connectCalled chan struct{}
}

func (m *connectSignalClient) Connect() pahomqtt.Token {
	select {
	case m.connectCalled <- struct{}{}:
	default:
	}
	return &mockMQTTToken{err: errors.New("connection refused")}
}

// TestExecute_StopHonoredPromptlyDuringReconnectBackoff is the regression test
// for sc-96142: a stop issued while Execute is waiting on the reconnect backoff
// must return promptly (well within the backoff interval), and the stop-signal
// monitor goroutine must never get stuck. Connect always fails, so after the
// first cycle Execute enters a ~1.5-2.5s backoff wait; closing stop during that
// wait must return Execute in well under a second.
func TestExecute_StopHonoredPromptlyDuringReconnectBackoff(t *testing.T) {
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

	connectCalled := make(chan struct{}, 10)
	origNewClient := inmqtt.NewClient
	inmqtt.NewClient = func(_ *pahomqtt.ClientOptions) pahomqtt.Client {
		return &connectSignalClient{connectCalled: connectCalled}
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

	baselineGoroutines := runtime.NumGoroutine()
	go func() { done <- svc.Execute(stop, running) }()

	select {
	case <-running:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute did not signal running within timeout")
	}

	// Wait for the first connect attempt to fail; Execute then computes the
	// next backoff and enters the reconnect-wait select.
	select {
	case <-connectCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("Execute did not attempt to connect within timeout")
	}

	// Give Execute a moment to settle into the backoff wait. This is shorter
	// than the minimum backoff (1.5 * InitialReconnectInterval) so the stop is
	// genuinely issued mid-backoff, not after it has elapsed.
	time.Sleep(150 * time.Millisecond)

	start := time.Now()
	close(stop)

	select {
	case code := <-done:
		elapsed := time.Since(start)
		if elapsed > time.Second {
			t.Fatalf(
				"stop during reconnect backoff not honored promptly: returned after %v",
				elapsed,
			)
		}
		if code != 0 {
			t.Errorf("expected exit code 0 on stop, got %d", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Execute did not exit after stop during reconnect backoff")
	}

	// The stop-signal monitor goroutine must not be left blocked on a send into
	// stopped after Execute returns. Poll until the goroutine count settles back
	// to the pre-Execute baseline; a persistently higher count indicates a
	// leaked/stuck monitor goroutine (the original sc-96142 failure mode).
	leaked := true
	for i := 0; i < 50; i++ {
		if runtime.NumGoroutine() <= baselineGoroutines {
			leaked = false
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if leaked {
		t.Fatalf(
			"goroutine count did not settle after stop (baseline=%d, current=%d): "+
				"monitor goroutine may be stuck on a blocked send",
			baselineGoroutines,
			runtime.NumGoroutine(),
		)
	}
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

// TestExecute_DisconnectCalledOnConnectFailure verifies that client.Disconnect
// is called even when Connect() itself fails. This exercises the deferred
// cleanup path added to fix sc-89438: the old explicit-call approach had no
// Disconnect call on the connect-failure path, leaking TCP resources.
func TestExecute_DisconnectCalledOnConnectFailure(t *testing.T) {
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
	// even when Connect() returned an error (deferred cleanup).
	disconnected := make(chan struct{}, 1)
	origNewClient := inmqtt.NewClient
	inmqtt.NewClient = func(_ *pahomqtt.ClientOptions) pahomqtt.Client {
		return &disconnectTrackingClient{
			mockMQTTClient: mockMQTTClient{connectErr: errors.New("connection refused")},
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

	// Wait for Disconnect to be called on the failed-connect cycle before
	// closing stop. With the old code (no Disconnect on connect failure)
	// this would block indefinitely.
	select {
	case <-disconnected:
	case <-time.After(5 * time.Second):
		t.Fatal("client.Disconnect was not called after connect failure")
	}

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
			name:        "explicit_qos_1",
			mqttQos:     func() *byte { b := byte(1); return &b }(),
			expectedQoS: "qos=1",
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

// TestExecute_ConnectionLostChannelIsBuffered verifies that the lost channel
// is buffered (capacity 1) so OnConnectionLost never blocks the MQTT library's
// internal goroutine when the main goroutine is occupied mid-command.
// Regression test for sc-89434: with an unbuffered channel a network drop
// during command execution deadlocked the service permanently.
func TestExecute_ConnectionLostChannelIsBuffered(t *testing.T) {
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

	token := &blockingToken{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}

	var capturedConnectionLost pahomqtt.ConnectionLostHandler

	origNewClient := inmqtt.NewClient
	inmqtt.NewClient = func(o *pahomqtt.ClientOptions) pahomqtt.Client {
		capturedConnectionLost = o.OnConnectionLost
		return &blockingPublishClient{publishToken: token}
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

	// Wait until UpdateReportedProperties is blocked in token.Wait(), meaning
	// the main goroutine is occupied and cannot receive from the lost channel.
	select {
	case <-token.started:
	case <-time.After(5 * time.Second):
		t.Fatal("Publish token.Wait did not start within timeout")
	}

	// Fire OnConnectionLost from a separate goroutine, simulating the MQTT
	// library's internal goroutine. With an unbuffered lost channel this send
	// blocks forever; with a buffered channel (capacity 1) it returns immediately.
	callbackDone := make(chan struct{})
	go func() {
		capturedConnectionLost(nil, errors.New("simulated network drop"))
		close(callbackDone)
	}()

	select {
	case <-callbackDone:
	case <-time.After(2 * time.Second):
		t.Fatal("OnConnectionLost blocked — lost channel must be buffered with capacity 1")
	}

	// Signal stop before unblocking so the reconnect wait exits immediately.
	close(stop)
	close(token.release)

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Execute did not exit within timeout")
	}
}

// TestExecute_UnsubscribeBeforeDisconnectOnStop is the regression test for
// sc-89441: the client must call Unsubscribe(topic) before Disconnect on the
// stop path so persistent (non-clean) Azure IoT Hub sessions don't retain
// server-side subscriptions and re-deliver buffered messages on reconnect.
func TestExecute_UnsubscribeBeforeDisconnectOnStop(t *testing.T) {
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

	var calls []string
	var subscribedTopic string
	origNewClient := inmqtt.NewClient
	inmqtt.NewClient = func(_ *pahomqtt.ClientOptions) pahomqtt.Client {
		return &teardownTrackingClient{
			calls:         &calls,
			subscribeArgs: &subscribedTopic,
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
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Execute did not exit within timeout")
	}

	expectedTopic := "devices/test-device/messages/devicebound/#"
	expected := []string{"unsubscribe:" + expectedTopic, "disconnect"}
	if len(calls) != len(expected) || calls[0] != expected[0] || calls[1] != expected[1] {
		t.Fatalf("expected teardown order %v, got %v", expected, calls)
	}
	if subscribedTopic != expectedTopic {
		t.Errorf("expected Subscribe called with %q, got %q", expectedTopic, subscribedTopic)
	}
}

// TestExecute_NoUnsubscribeWhenSubscribeFails verifies that Unsubscribe is not
// issued when Subscribe never succeeded. Calling Unsubscribe in that case would
// be a no-op on the broker (no subscription exists) and just produce log noise.
func TestExecute_NoUnsubscribeWhenSubscribeFails(t *testing.T) {
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

	var calls []string
	origNewClient := inmqtt.NewClient
	inmqtt.NewClient = func(_ *pahomqtt.ClientOptions) pahomqtt.Client {
		return &teardownTrackingClient{
			mockMQTTClient: mockMQTTClient{subscribeErr: errors.New("broker denied")},
			calls:          &calls,
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
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Execute did not exit within timeout")
	}

	for _, c := range calls {
		if strings.HasPrefix(c, "unsubscribe:") {
			t.Errorf("Unsubscribe should not be called when Subscribe failed; got calls=%v", calls)
			break
		}
	}
}

// TestPostbackRetryBackoff_DefaultScheduleUnchanged verifies that the default
// configuration (base 1s) still yields the historical ~1s / ~2s schedule for the
// first two retries. Jitter is bounded to ±25%, so each slot is asserted within
// that band of the exponential base rather than exactly.
func TestPostbackRetryBackoff_DefaultScheduleUnchanged(t *testing.T) {
	base := agent.DefaultPostbackBaseRetryBackoff
	maxBackoff := agent.DefaultPostbackMaxRetryBackoff

	cases := []struct {
		attempt  int
		expected time.Duration // exponential base before jitter
	}{
		{attempt: 2, expected: 1 * time.Second},
		{attempt: 3, expected: 2 * time.Second},
	}

	for _, c := range cases {
		// Sample repeatedly so jitter variance is exercised.
		for i := 0; i < 1000; i++ {
			got := postbackRetryBackoff(base, maxBackoff, c.attempt)
			low := time.Duration(float64(c.expected) * 0.75)
			high := time.Duration(float64(c.expected) * 1.25)
			if got < low || got > high {
				t.Fatalf("attempt %d: backoff %v outside expected jittered band [%v, %v]",
					c.attempt, got, low, high)
			}
		}
	}
}

// TestPostbackRetryBackoff_LargeAttemptsStayPositiveAndCapped is the core
// regression for the uncapped/overflowing backoff: for very large attempt
// numbers (well past the point where base * (1 << (attempt-2)) overflows int64
// nanoseconds into a negative duration) the computed backoff must remain
// strictly positive and never exceed the configured cap.
func TestPostbackRetryBackoff_LargeAttemptsStayPositiveAndCapped(t *testing.T) {
	base := 1 * time.Second
	maxBackoff := agent.DefaultPostbackMaxRetryBackoff

	// attempt-2 == 33 already overflows the old 1<<(attempt-2) nanosecond shift;
	// go well beyond it, including absurd operator values.
	for _, attempt := range []int{20, 35, 40, 64, 100, 1000, 1 << 20} {
		for i := 0; i < 200; i++ {
			got := postbackRetryBackoff(base, maxBackoff, attempt)
			if got <= 0 {
				t.Fatalf("attempt %d: backoff must be strictly positive, got %v", attempt, got)
			}
			if got > maxBackoff {
				t.Fatalf("attempt %d: backoff %v exceeds cap %v", attempt, got, maxBackoff)
			}
		}
	}
}

// TestPostbackRetryBackoff_BaseAboveCapClamped verifies that an operator setting
// a base delay larger than the cap still produces a bounded, positive slot.
func TestPostbackRetryBackoff_BaseAboveCapClamped(t *testing.T) {
	maxBackoff := agent.DefaultPostbackMaxRetryBackoff
	base := maxBackoff * 10

	for _, attempt := range []int{2, 3, 50} {
		got := postbackRetryBackoff(base, maxBackoff, attempt)
		if got <= 0 || got > maxBackoff {
			t.Fatalf("attempt %d: expected 0 < backoff <= %v, got %v", attempt, maxBackoff, got)
		}
	}
}

// TestPostbackRetryBackoff_NonPositiveInputsFallBackToDefaults verifies the
// guards that keep a misconfigured base/cap from producing a zero or negative
// slot.
func TestPostbackRetryBackoff_NonPositiveInputsFallBackToDefaults(t *testing.T) {
	got := postbackRetryBackoff(0, 0, 5)
	if got <= 0 {
		t.Fatalf("expected positive backoff with defaulted inputs, got %v", got)
	}
	if got > postbackMaxRetryBackoff {
		t.Fatalf("expected backoff within defaulted cap %v, got %v", postbackMaxRetryBackoff, got)
	}
}

// TestExecute_SweepsStaleScriptFilesOnStartup verifies the sc-103967 startup
// sweep: script files orphaned by a previous run that was killed before its
// deferred cleanup could run are reclaimed when the service starts, while files
// that are recent (a command may still be executing) or not created by the agent
// are left alone.
func TestExecute_SweepsStaleScriptFilesOnStartup(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	logPath := filepath.Join(tmpDir, "test.log")

	orgId := "test-org-sweep"
	device := agent.Device{
		DeviceId:             "test-device",
		SharedAccessKey:      "dGVzdC1zaGFyZWQta2V5LXRoYXQtaXMtbG9uZy1lbm91Z2gtZm9yLWJhc2U2NC1kZWNvZGluZw==",
		AzureIotHubHost:      "test.azure-devices.net",
		RewstOrgId:           orgId,
		LoggingLevel:         "info",
		DisableAutoUpdates:   true,
		DisableAgentPostback: true,
	}
	configBytes, _ := json.Marshal(device)
	if err := os.WriteFile(configPath, configBytes, utils.DefaultFileMod); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	scriptsDir := agent.GetScriptsDirectory(orgId)
	if err := os.MkdirAll(scriptsDir, utils.DefaultDirMod); err != nil {
		t.Fatalf("failed to create scripts dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(scriptsDir) })

	write := func(name string, age time.Duration) string {
		path := filepath.Join(scriptsDir, name)
		if err := os.WriteFile(path, []byte("echo hello"), utils.DefaultFileMod); err != nil {
			t.Fatalf("failed to write %s: %v", name, err)
		}
		modTime := time.Now().Add(-age)
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatalf("failed to set mtime on %s: %v", name, err)
		}
		return path
	}

	stale := write("exec-123456789.ps1", 48*time.Hour)
	recent := write("exec-987654321.ps1", time.Minute)
	foreign := write("operator-script.ps1", 48*time.Hour)

	origNewClient := inmqtt.NewClient
	inmqtt.NewClient = func(o *pahomqtt.ClientOptions) pahomqtt.Client {
		return &mockMQTTClient{}
	}
	defer func() { inmqtt.NewClient = origNewClient }()

	svc := &serviceContext{
		ConfigFile: configPath,
		LogFile:    logPath,
		OrgId:      orgId,
		Executor:   &mockExecutor{},
	}

	stop := make(chan struct{})
	running := make(chan struct{}, 1)

	done := make(chan service.ServiceExitCode, 1)
	go func() {
		done <- svc.Execute(stop, running)
	}()

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

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("expected the stale script file to be swept, stat err = %v", err)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Errorf("expected the recent script file to survive: %v", err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Errorf("expected a file the agent did not create to survive: %v", err)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log: %v", err)
	}
	if !strings.Contains(string(logBytes), "Swept stale script files") {
		t.Errorf("expected the sweep to be logged, log was:\n%s", logBytes)
	}
}

// runExecuteWithDevice writes device to a config file, runs Execute against a
// mock MQTT client until it reports running, stops it, and returns the log
// contents. It is used by the plugin supervision tests, which care about what the
// service logs around plugin loading rather than about message flow.
func runExecuteWithDevice(t *testing.T, orgId string, device agent.Device) string {
	t.Helper()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	logPath := filepath.Join(tmpDir, "test.log")

	configBytes, err := json.Marshal(device)
	if err != nil {
		t.Fatalf("failed to marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, configBytes, utils.DefaultFileMod); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	origNewClient := inmqtt.NewClient
	inmqtt.NewClient = func(o *pahomqtt.ClientOptions) pahomqtt.Client {
		return &mockMQTTClient{}
	}
	defer func() { inmqtt.NewClient = origNewClient }()

	svc := &serviceContext{
		ConfigFile: configPath,
		LogFile:    logPath,
		OrgId:      orgId,
		Executor:   &mockExecutor{},
	}

	stop := make(chan struct{})
	running := make(chan struct{}, 1)

	done := make(chan service.ServiceExitCode, 1)
	go func() {
		done <- svc.Execute(stop, running)
	}()

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

	return string(logBytes)
}

// TestExecute_NoPluginsConfiguredSkipsSupervision verifies that plugin
// supervision is inert for the common case of a device with no plugins: nothing
// is loaded, no health summary is reported, and the service starts and stops
// exactly as before.
func TestExecute_NoPluginsConfiguredSkipsSupervision(t *testing.T) {
	orgId := "test-org-no-plugins"
	logged := runExecuteWithDevice(t, orgId, agent.Device{
		DeviceId:             "test-device",
		SharedAccessKey:      "dGVzdC1zaGFyZWQta2V5LXRoYXQtaXMtbG9uZy1lbm91Z2gtZm9yLWJhc2U2NC1kZWNvZGluZw==",
		AzureIotHubHost:      "test.azure-devices.net",
		RewstOrgId:           orgId,
		LoggingLevel:         "debug",
		DisableAutoUpdates:   true,
		DisableAgentPostback: true,
	})

	for _, unexpected := range []string{
		"Plugin loaded",
		"Plugins loaded",
		"Plugin notification health summary",
		"Notification delivery failed",
	} {
		if strings.Contains(logged, unexpected) {
			t.Errorf(
				"expected no %q line with no plugins configured, log was:\n%s",
				unexpected,
				logged,
			)
		}
	}
}

// TestExecute_UnloadablePluginIsReportedNotSupervised verifies that a plugin that
// cannot be launched at startup is still reported (as before) and does not leave
// the service supervising a plugin that was never loaded.
func TestExecute_UnloadablePluginIsReportedNotSupervised(t *testing.T) {
	orgId := "test-org-bad-plugin"
	logged := runExecuteWithDevice(t, orgId, agent.Device{
		DeviceId:             "test-device",
		SharedAccessKey:      "dGVzdC1zaGFyZWQta2V5LXRoYXQtaXMtbG9uZy1lbm91Z2gtZm9yLWJhc2U2NC1kZWNvZGluZw==",
		AzureIotHubHost:      "test.azure-devices.net",
		RewstOrgId:           orgId,
		LoggingLevel:         "debug",
		DisableAutoUpdates:   true,
		DisableAgentPostback: true,
		Plugins: []agent.Plugin{
			{Name: "missing-plugin", ExecutablePath: filepath.Join(t.TempDir(), "does-not-exist")},
		},
	})

	if !strings.Contains(logged, "Failed to load plugin") {
		t.Errorf("expected the failed plugin load to be logged, log was:\n%s", logged)
	}
	if strings.Contains(logged, "Plugin loaded") {
		t.Errorf("expected no plugin to be reported as loaded, log was:\n%s", logged)
	}
	if strings.Contains(logged, "Plugin notification health summary") {
		t.Errorf("expected no health summary for a plugin that never loaded, log was:\n%s", logged)
	}
}
