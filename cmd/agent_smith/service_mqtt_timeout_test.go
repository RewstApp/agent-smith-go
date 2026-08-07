package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	inmqtt "github.com/RewstApp/agent-smith-go/internal/mqtt"
	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

// Regression tests for sc-106107: every paho token wait in the connection cycle
// must be bounded, and the subscribe wait must additionally be interruptible by
// the stop signal. Before the fix a broker that accepted CONNECT and withheld
// SUBACK — what Azure IoT Hub does when it throttles a device, and what a
// middlebox that half-opens a connection produces — parked the cycle in
// token.Wait() forever: the agent logged a successful connect, never subscribed,
// executed nothing, and could not be stopped because it never reached its select
// on the stop signal.

// hangingSubscribeClient returns a token that never resolves from Subscribe,
// simulating a broker that accepts the SUBSCRIBE and never sends SUBACK while
// keeping the connection otherwise healthy. Each Subscribe call gets its own
// token so successive connection cycles hang independently.
type hangingSubscribeClient struct {
	mockMQTTClient

	mu          sync.Mutex
	tokens      []*blockingToken
	subscribed  chan struct{}
	disconnects chan struct{}
}

func (m *hangingSubscribeClient) Subscribe(
	_ string,
	_ byte,
	_ pahomqtt.MessageHandler,
) pahomqtt.Token {
	token := &blockingToken{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}

	m.mu.Lock()
	m.tokens = append(m.tokens, token)
	m.mu.Unlock()

	select {
	case m.subscribed <- struct{}{}:
	default:
	}

	return token
}

func (m *hangingSubscribeClient) Disconnect(_ uint) {
	select {
	case m.disconnects <- struct{}{}:
	default:
	}
}

// releaseAll unblocks every token handed out so no goroutine parked on one
// outlives the test.
func (m *hangingSubscribeClient) releaseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, token := range m.tokens {
		close(token.release)
	}
	m.tokens = nil
}

// hangingUnsubscribeClient acknowledges the subscribe normally but returns a
// token that never resolves from Unsubscribe, simulating a connection that has
// been black-holed (packets dropped with no RST) by the time teardown runs.
type hangingUnsubscribeClient struct {
	mockMQTTClient
	unsubscribeToken *blockingToken
	disconnected     chan struct{}
}

func (m *hangingUnsubscribeClient) Unsubscribe(_ ...string) pahomqtt.Token {
	return m.unsubscribeToken
}

func (m *hangingUnsubscribeClient) Disconnect(_ uint) {
	select {
	case m.disconnected <- struct{}{}:
	default:
	}
}

// writeDeviceConfig writes device to a config file in a fresh temp directory and
// returns the config and log paths.
func writeDeviceConfig(t *testing.T, device agent.Device) (string, string) {
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

	return configPath, logPath
}

// timeoutTestDevice is the minimal device config these tests drive Execute with.
func timeoutTestDevice() agent.Device {
	return agent.Device{
		DeviceId:             "test-device",
		SharedAccessKey:      "dGVzdC1zaGFyZWQta2V5LXRoYXQtaXMtbG9uZy1lbm91Z2gtZm9yLWJhc2U2NC1kZWNvZGluZw==",
		AzureIotHubHost:      "test.azure-devices.net",
		LoggingLevel:         "info",
		DisableAutoUpdates:   true,
		DisableAgentPostback: true,
	}
}

// installMockClient points the MQTT client factory at newClient for the duration
// of the test.
func installMockClient(t *testing.T, newClient func() pahomqtt.Client) {
	t.Helper()

	orig := inmqtt.NewClient
	inmqtt.NewClient = func(_ *pahomqtt.ClientOptions) pahomqtt.Client { return newClient() }
	t.Cleanup(func() { inmqtt.NewClient = orig })
}

// waitForLog polls the log file until match reports satisfied or the deadline
// elapses, returning the log content it last read.
func waitForLog(
	t *testing.T,
	logPath string,
	timeout time.Duration,
	match func(string) bool,
) string {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var content string
	for {
		if b, err := os.ReadFile(logPath); err == nil {
			content = string(b)
			if match(content) {
				return content
			}
		}
		if time.Now().After(deadline) {
			return content
		}
		time.Sleep(20 * time.Millisecond)
	}
}

var reconnectTimeoutPattern = regexp.MustCompile(`Reconnecting in: timeout=([0-9a-z.µ]+)`)

// reconnectTimeouts extracts the backoff interval from every "Reconnecting in"
// line in the log, in order.
func reconnectTimeouts(t *testing.T, log string) []time.Duration {
	t.Helper()

	var out []time.Duration
	for _, m := range reconnectTimeoutPattern.FindAllStringSubmatch(log, -1) {
		d, err := time.ParseDuration(m[1])
		if err != nil {
			t.Fatalf("failed to parse reconnect timeout %q: %v", m[1], err)
		}
		out = append(out, d)
	}
	return out
}

// TestExecute_SubscribeTimeoutEndsCycleAndBacksOff verifies that a broker which
// never acknowledges the SUBSCRIBE ends the connection cycle within the
// configured timeout, logs the failure at Error with the elapsed timeout,
// disconnects, and hands the retry to the reconnect backoff schedule. The
// backoff must *not* be cleared: a throttling broker needs the agent to back
// off, not reconnect in a tight loop, so successive reconnect waits must grow.
func TestExecute_SubscribeTimeoutEndsCycleAndBacksOff(t *testing.T) {
	subscribeTimeoutSeconds := 1
	device := timeoutTestDevice()
	device.MqttSubscribeTimeoutSeconds = &subscribeTimeoutSeconds
	configPath, logPath := writeDeviceConfig(t, device)

	client := &hangingSubscribeClient{
		subscribed:  make(chan struct{}, 1),
		disconnects: make(chan struct{}, 4),
	}
	t.Cleanup(client.releaseAll)
	installMockClient(t, func() pahomqtt.Client { return client })

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

	// Two failed cycles are needed to observe the backoff growing. Each costs the
	// 1s subscribe timeout plus its reconnect wait (~2s then ~4s).
	logged := waitForLog(t, logPath, 20*time.Second, func(s string) bool {
		return len(reconnectTimeouts(t, s)) >= 2
	})

	close(stop)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Execute did not exit within timeout after subscribe timeouts")
	}

	if !strings.Contains(
		logged,
		"Failed to subscribe: timed out waiting for broker acknowledgement",
	) {
		t.Fatalf("expected a bounded subscribe failure to be logged, log was:\n%s", logged)
	}
	if !strings.Contains(logged, "timeout=1s") {
		t.Errorf("expected the subscribe timeout to be logged, log was:\n%s", logged)
	}
	if !strings.Contains(logged, "elapsed=") {
		t.Errorf("expected the elapsed wait to be logged, log was:\n%s", logged)
	}
	if strings.Contains(logged, "Subscribed to messages") {
		t.Errorf("expected no successful subscription, log was:\n%s", logged)
	}

	// The cycle must have torn down its client rather than leaking a connected
	// but unsubscribed one.
	select {
	case <-client.disconnects:
	default:
		t.Error("expected the timed-out cycle to disconnect its client")
	}

	timeouts := reconnectTimeouts(t, logged)
	if len(timeouts) < 2 {
		t.Fatalf("expected at least two reconnect waits, got %v; log was:\n%s", timeouts, logged)
	}
	if timeouts[1] <= timeouts[0] {
		t.Errorf(
			"expected the reconnect backoff to grow after a subscribe timeout "+
				"(it must not be cleared), got %v then %v",
			timeouts[0],
			timeouts[1],
		)
	}
}

// TestExecute_StopHonoredDuringHangingSubscribe verifies that a stop issued
// while the agent is waiting for SUBACK returns from Execute promptly rather
// than after the subscribe timeout. The device uses the default 30s subscribe
// timeout, so a prompt return can only come from the wait being interruptible.
func TestExecute_StopHonoredDuringHangingSubscribe(t *testing.T) {
	device := timeoutTestDevice()
	configPath, logPath := writeDeviceConfig(t, device)

	client := &hangingSubscribeClient{
		subscribed:  make(chan struct{}, 1),
		disconnects: make(chan struct{}, 4),
	}
	t.Cleanup(client.releaseAll)
	installMockClient(t, func() pahomqtt.Client { return client })

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

	// Wait until the cycle is actually parked waiting for SUBACK.
	select {
	case <-client.subscribed:
	case <-time.After(5 * time.Second):
		t.Fatal("Subscribe was never called")
	}

	start := time.Now()
	close(stop)

	select {
	case code := <-done:
		elapsed := time.Since(start)
		// Comfortably inside the Windows SCM 30s stop window and far short of the
		// 30s default subscribe timeout, proving the wait was interrupted rather
		// than timed out.
		if elapsed > 2*time.Second {
			t.Fatalf(
				"stop during a hanging subscribe not honored promptly: returned after %v",
				elapsed,
			)
		}
		if code != 0 {
			t.Errorf("expected exit code 0 on stop, got %d", code)
		}
	case <-time.After(utils.DefaultMqttSubscribeTimeout):
		t.Fatal("Execute never returned after a stop during a hanging subscribe")
	}

	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log: %v", err)
	}
	if !strings.Contains(string(logged), "Subscribe abandoned: service is stopping") {
		t.Errorf("expected the abandoned subscribe to be logged, log was:\n%s", logged)
	}

	select {
	case <-client.disconnects:
	default:
		t.Error("expected the abandoned cycle to disconnect its client")
	}
}

// TestExecute_HangingUnsubscribeStillDisconnects verifies that an UNSUBACK that
// never arrives during teardown is bounded: it warns and proceeds to Disconnect
// instead of holding the service past the platform's stop deadline.
func TestExecute_HangingUnsubscribeStillDisconnects(t *testing.T) {
	device := timeoutTestDevice()
	configPath, logPath := writeDeviceConfig(t, device)

	unsubscribeToken := &blockingToken{
		started: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
	t.Cleanup(func() { close(unsubscribeToken.release) })

	client := &hangingUnsubscribeClient{
		unsubscribeToken: unsubscribeToken,
		disconnected:     make(chan struct{}, 1),
	}
	installMockClient(t, func() pahomqtt.Client { return client })

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

	// Let the cycle subscribe and settle on its stop select before stopping, so
	// teardown genuinely reaches the unsubscribe.
	waitForLog(t, logPath, 5*time.Second, func(s string) bool {
		return strings.Contains(s, "Subscribed to messages")
	})

	start := time.Now()
	close(stop)

	select {
	case code := <-done:
		elapsed := time.Since(start)
		// The unsubscribe alone is bounded at utils.MqttUnsubscribeTimeout; the
		// rest of teardown is prompt. A generous ceiling still proves the wait is
		// bounded rather than open-ended.
		if elapsed > utils.MqttUnsubscribeTimeout+5*time.Second {
			t.Fatalf("teardown with a hanging unsubscribe took %v", elapsed)
		}
		if code != 0 {
			t.Errorf("expected exit code 0 on stop, got %d", code)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Execute never returned with a hanging unsubscribe")
	}

	select {
	case <-client.disconnected:
	default:
		t.Fatal("expected Disconnect to run even though the unsubscribe never completed")
	}

	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log: %v", err)
	}
	if !strings.Contains(
		string(logged),
		"Timed out waiting for unsubscribe; disconnecting anyway",
	) {
		t.Errorf("expected the unsubscribe timeout to be warned about, log was:\n%s", logged)
	}
}

// TestExecute_FastAckSubscribeUnchanged verifies the happy path is untouched: a
// broker that acknowledges promptly still produces a normal subscription with no
// timeout diagnostics, and the cycle proceeds to its stop select.
func TestExecute_FastAckSubscribeUnchanged(t *testing.T) {
	device := timeoutTestDevice()
	configPath, logPath := writeDeviceConfig(t, device)

	installMockClient(t, func() pahomqtt.Client { return &mockMQTTClient{} })

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

	logged := waitForLog(t, logPath, 5*time.Second, func(s string) bool {
		return strings.Contains(s, "Subscribed to messages")
	})

	start := time.Now()
	close(stop)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Execute did not exit within timeout")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("expected a prompt stop on the happy path, took %v", elapsed)
	}

	if !strings.Contains(logged, "Subscribed to messages") {
		t.Fatalf("expected a normal subscription, log was:\n%s", logged)
	}
	for _, unexpected := range []string{
		"Failed to subscribe",
		"Failed to connect",
		"Timed out waiting for unsubscribe",
		"Subscribe abandoned",
		"Connect abandoned",
		"Reconnecting in",
	} {
		if strings.Contains(logged, unexpected) {
			t.Errorf("unexpected %q on the fast-ACK path, log was:\n%s", unexpected, logged)
		}
	}
}
