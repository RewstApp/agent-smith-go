package plugins

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/rpc"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/shared"
	"github.com/hashicorp/go-hclog"
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

	wrapper, err := LoadNotifer(plugins, logBuf, hclog.NewNullLogger())
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

	wrapper, err := LoadNotifer(plugins, logBuf, hclog.NewNullLogger())

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

	wrapper, err := LoadNotifer(plugins, logBuf, hclog.NewNullLogger())

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

	wrapper, err := LoadNotifer(plugins, nil, nil)

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

// TestToNotifier_ValidNotifier tests that a valid shared.Notifier passes the assertion
func TestToNotifier_ValidNotifier(t *testing.T) {
	mock := &mockNotifier{}
	notifier, ok := toNotifier(mock)
	if !ok {
		t.Fatal("expected ok=true for a valid shared.Notifier")
	}
	if notifier == nil {
		t.Fatal("expected non-nil notifier")
	}
}

// TestToNotifier_NonNotifierType tests that an incompatible type fails the assertion
func TestToNotifier_NonNotifierType(t *testing.T) {
	notifier, ok := toNotifier("not a notifier")
	if ok {
		t.Fatal("expected ok=false for a non-Notifier type")
	}
	if notifier != nil {
		t.Fatal("expected nil notifier on failed assertion")
	}
}

// TestToNotifier_Nil tests that a nil interface fails the assertion
func TestToNotifier_Nil(t *testing.T) {
	notifier, ok := toNotifier(nil)
	if ok {
		t.Fatal("expected ok=false for nil")
	}
	if notifier != nil {
		t.Fatal("expected nil notifier")
	}
}

// syncBuffer is a concurrency-safe log sink. go-plugin drains the plugin's
// stderr on its own goroutine, so a buffer shared with the host logger must be
// locked.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// newBufferedLogger returns a logger writing to out so tests can assert on the
// log lines emitted for failures, restarts and recoveries.
func newBufferedLogger(out io.Writer) hclog.Logger {
	return hclog.New(&hclog.LoggerOptions{
		Name:   "test",
		Output: out,
		Level:  hclog.Debug,
	})
}

// countLines counts how many times substr appears at the start of a log line.
func countLines(logged, substr string) int {
	count := 0
	for _, line := range strings.Split(logged, "\n") {
		if strings.Contains(line, substr) {
			count++
		}
	}
	return count
}

// TestIsTransportError distinguishes a broken RPC channel (the subprocess is
// gone) from an error the plugin itself returned.
func TestIsTransportError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil", nil, false},
		{"plugin_returned_error", rpc.ServerError("webhook rejected"), false},
		{"wrapped_plugin_error", fmt.Errorf("notify: %w", rpc.ServerError("nope")), false},
		{"rpc_shutdown", rpc.ErrShutdown, true},
		{"eof", io.EOF, true},
		{"generic", errors.New("broken pipe"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isTransportError(tt.err); got != tt.expected {
				t.Errorf("isTransportError(%v) = %v, want %v", tt.err, got, tt.expected)
			}
		})
	}
}

// TestOptionalNotifierWrapper_Notify_TransportErrorDropsHandle verifies that a
// broken RPC channel drops the plugin handle so the next attempt relaunches the
// subprocess instead of calling a dead client forever.
func TestOptionalNotifierWrapper_Notify_TransportErrorDropsHandle(t *testing.T) {
	mock := &mockNotifier{notifyErr: rpc.ErrShutdown}
	wrapper := &optionalNotifierWrapper{plugin: mock, name: "test"}

	if err := wrapper.Notify("hello"); err == nil {
		t.Fatal("expected an error from a broken RPC channel")
	}

	if wrapper.plugin != nil {
		t.Error("expected the plugin handle to be dropped after a transport error")
	}

	if got := wrapper.Stats().NotifyFailures; got != 1 {
		t.Errorf("expected 1 notify failure, got %d", got)
	}
}

// TestOptionalNotifierWrapper_Notify_PluginErrorKeepsHandle verifies that an
// error the plugin itself returned is counted but does not tear down a healthy
// subprocess.
func TestOptionalNotifierWrapper_Notify_PluginErrorKeepsHandle(t *testing.T) {
	mock := &mockNotifier{notifyErr: rpc.ServerError("webhook rejected")}
	wrapper := &optionalNotifierWrapper{plugin: mock, name: "test"}

	if err := wrapper.Notify("hello"); err == nil {
		t.Fatal("expected the plugin error to be returned")
	}

	if wrapper.plugin == nil {
		t.Error("expected a plugin-side error to leave the subprocess handle in place")
	}

	if got := wrapper.Stats().NotifyFailures; got != 1 {
		t.Errorf("expected 1 notify failure, got %d", got)
	}
}

// TestOptionalNotifierWrapper_Notify_TimeoutDropsHandleAndCounts verifies a
// Notify call that times out (the "hung, not exited" case the crash-only health
// check cannot see) is treated like a broken transport: the handle is dropped so
// the next attempt relaunches the subprocess, and the timeout is counted both as
// a general failure and, distinctly, as a timeout.
func TestOptionalNotifierWrapper_Notify_TimeoutDropsHandleAndCounts(t *testing.T) {
	mock := &mockNotifier{notifyErr: shared.ErrNotifyTimeout}
	wrapper := &optionalNotifierWrapper{plugin: mock, name: "test"}

	if err := wrapper.Notify("hello"); !errors.Is(err, shared.ErrNotifyTimeout) {
		t.Fatalf("expected ErrNotifyTimeout, got %v", err)
	}

	if wrapper.plugin != nil {
		t.Error("expected the plugin handle to be dropped after a timeout")
	}

	stats := wrapper.Stats()
	if stats.NotifyFailures != 1 {
		t.Errorf("expected 1 notify failure, got %d", stats.NotifyFailures)
	}
	if stats.NotifyTimeouts != 1 {
		t.Errorf("expected 1 notify timeout, got %d", stats.NotifyTimeouts)
	}
}

// TestOptionalNotifierWrapper_Notify_TimeoutLoggedDistinctlyFromCrash verifies a
// timeout and a crash produce distinguishable failure log lines and counters, so
// QA and on-call can tell the two failure modes apart.
func TestOptionalNotifierWrapper_Notify_TimeoutLoggedDistinctlyFromCrash(t *testing.T) {
	logBuf := &bytes.Buffer{}
	mock := &mockNotifier{notifyErr: shared.ErrNotifyTimeout}
	wrapper := &optionalNotifierWrapper{
		plugin: mock,
		name:   "test",
		logger: newBufferedLogger(logBuf),
	}

	if err := wrapper.Notify("hello"); err == nil {
		t.Fatal("expected the timeout to be returned")
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "RPC call timed out") {
		t.Errorf("expected the timeout error text in the failure log, got %q", logged)
	}

	stats := wrapper.Stats()
	if stats.NotifyFailures != 1 || stats.NotifyTimeouts != 1 {
		t.Errorf(
			"expected NotifyFailures=1 and NotifyTimeouts=1 for a timeout, got %+v",
			stats,
		)
	}

	// A crash (transport error) is counted as a general failure but must not move
	// the timeout-specific counter.
	mock.notifyErr = rpc.ErrShutdown
	wrapper.plugin = mock
	if err := wrapper.Notify("hello"); err == nil {
		t.Fatal("expected the crash to be returned")
	}

	stats = wrapper.Stats()
	if stats.NotifyFailures != 2 {
		t.Errorf("expected NotifyFailures=2 after a second failure, got %d", stats.NotifyFailures)
	}
	if stats.NotifyTimeouts != 1 {
		t.Errorf(
			"expected NotifyTimeouts to stay at 1 after a non-timeout failure, got %d",
			stats.NotifyTimeouts,
		)
	}
}

// TestOptionalNotifierWrapper_Notify_LogsOncePerFailureTransition verifies the
// counter increments on every failure while the log stays quiet until the plugin
// recovers, so a persistently broken plugin cannot flood the agent log.
func TestOptionalNotifierWrapper_Notify_LogsOncePerFailureTransition(t *testing.T) {
	logBuf := &bytes.Buffer{}
	mock := &mockNotifier{notifyErr: rpc.ServerError("still broken")}
	wrapper := &optionalNotifierWrapper{
		plugin: mock,
		name:   "test",
		logger: newBufferedLogger(logBuf),
	}

	for range 3 {
		if err := wrapper.Notify("hello"); err == nil {
			t.Fatal("expected notify to fail")
		}
	}

	if got := wrapper.Stats().NotifyFailures; got != 3 {
		t.Errorf("expected 3 notify failures counted, got %d", got)
	}

	if got := countLines(logBuf.String(), "Notification delivery failed"); got != 1 {
		t.Errorf("expected exactly 1 failure log line for 3 failures, got %d", got)
	}

	// Recovery closes out the failure, and the next failure run logs again.
	mock.notifyErr = nil
	if err := wrapper.Notify("hello"); err != nil {
		t.Fatalf("expected recovery notify to succeed, got %v", err)
	}

	if got := countLines(logBuf.String(), "Notification delivery recovered"); got != 1 {
		t.Errorf("expected 1 recovery log line, got %d", got)
	}

	mock.notifyErr = rpc.ServerError("broken again")
	if err := wrapper.Notify("hello"); err == nil {
		t.Fatal("expected notify to fail again")
	}

	if got := countLines(logBuf.String(), "Notification delivery failed"); got != 2 {
		t.Errorf("expected a second failure log line after recovery, got %d", got)
	}
}

// TestOptionalNotifierWrapper_Notify_NotRunningIsSurfaced verifies that a
// configured plugin with no live subprocess reports the missed notification
// instead of silently discarding it.
func TestOptionalNotifierWrapper_Notify_NotRunningIsSurfaced(t *testing.T) {
	logBuf := &bytes.Buffer{}
	wrapper := &optionalNotifierWrapper{
		name:   "test",
		info:   agent.Plugin{Name: "test", ExecutablePath: "/nonexistent/path/to/plugin"},
		logger: newBufferedLogger(logBuf),
	}

	err := wrapper.Notify("hello")
	if err == nil {
		t.Fatal("expected an error when the configured plugin is not running")
	}

	stats := wrapper.Stats()
	if stats.NotifyFailures != 1 {
		t.Errorf("expected 1 notify failure, got %d", stats.NotifyFailures)
	}
	if stats.RestartFailures != 1 {
		t.Errorf("expected 1 restart failure, got %d", stats.RestartFailures)
	}

	logged := logBuf.String()
	if !strings.Contains(logged, "Failed to restart notification plugin") {
		t.Errorf("expected the failed relaunch to be logged, got %q", logged)
	}
	if !strings.Contains(logged, "Notification delivery failed") {
		t.Errorf("expected the missed notification to be logged, got %q", logged)
	}
}

// TestOptionalNotifierWrapper_CheckHealth_RestartBackoff verifies a plugin that
// cannot be relaunched is retried on a backoff schedule rather than re-spawned on
// every check.
func TestOptionalNotifierWrapper_CheckHealth_RestartBackoff(t *testing.T) {
	wrapper := &optionalNotifierWrapper{
		name:           "test",
		info:           agent.Plugin{Name: "test", ExecutablePath: "/nonexistent/path/to/plugin"},
		restartBackoff: restartBaseBackoff,
	}

	wrapper.CheckHealth()
	if got := wrapper.Stats().RestartFailures; got != 1 {
		t.Fatalf("expected 1 restart attempt, got %d", got)
	}

	// An immediate second check falls inside the backoff window.
	wrapper.CheckHealth()
	if got := wrapper.Stats().RestartFailures; got != 1 {
		t.Errorf("expected the second check to be deferred by backoff, got %d attempts", got)
	}

	if wrapper.restartBackoff <= restartBaseBackoff {
		t.Errorf(
			"expected the backoff to grow after a failed restart, got %v",
			wrapper.restartBackoff,
		)
	}
}

// TestOptionalNotifierWrapper_CheckHealth_StableUptimeResetsBackoff verifies that
// a plugin which ran past the stability window is relaunched immediately when it
// dies, rather than waiting out a backoff inherited from an earlier crash.
func TestOptionalNotifierWrapper_CheckHealth_StableUptimeResetsBackoff(t *testing.T) {
	wrapper := &optionalNotifierWrapper{
		name:           "test",
		info:           agent.Plugin{Name: "test", ExecutablePath: "/nonexistent/path/to/plugin"},
		startedAt:      time.Now().Add(-2 * restartStableUptime),
		nextRestartAt:  time.Now().Add(restartMaxBackoff),
		restartBackoff: restartMaxBackoff,
	}

	wrapper.CheckHealth()

	if got := wrapper.Stats().RestartFailures; got != 1 {
		t.Errorf("expected an immediate relaunch attempt after stable uptime, got %d", got)
	}
}

// TestOptionalNotifierWrapper_CheckHealth_NotRestartable verifies a wrapper with
// no plugin behind it is left alone.
func TestOptionalNotifierWrapper_CheckHealth_NotRestartable(t *testing.T) {
	wrapper := &optionalNotifierWrapper{name: "test"}

	wrapper.CheckHealth()

	if stats := wrapper.Stats(); stats != (NotifierStats{}) {
		t.Errorf("expected no activity for a wrapper with no plugin, got %+v", stats)
	}
}

// TestOptionalNotifierWrapper_CheckHealth_AfterKill verifies teardown wins: a
// health check racing with shutdown must not resurrect the plugin.
func TestOptionalNotifierWrapper_CheckHealth_AfterKill(t *testing.T) {
	wrapper := &optionalNotifierWrapper{
		name:           "test",
		info:           agent.Plugin{Name: "test", ExecutablePath: "/nonexistent/path/to/plugin"},
		restartBackoff: restartBaseBackoff,
	}

	wrapper.Kill()
	wrapper.CheckHealth()

	if got := wrapper.Stats().RestartFailures; got != 0 {
		t.Errorf("expected no relaunch attempt after Kill, got %d", got)
	}

	if err := wrapper.Notify("hello"); err != nil {
		t.Errorf("expected Notify after Kill to be a no-op, got %v", err)
	}
	if got := wrapper.Stats().NotifyFailures; got != 0 {
		t.Errorf("expected no notify failures counted after Kill, got %d", got)
	}
}

// TestNotifierSetWrapper_CheckHealth_Empty tests CheckHealth with an empty set
func TestNotifierSetWrapper_CheckHealth_Empty(t *testing.T) {
	set := &notifierSetWrapper{}

	// Should not panic
	set.CheckHealth()

	if stats := set.Stats(); stats != (NotifierStats{}) {
		t.Errorf("expected zero stats for an empty set, got %+v", stats)
	}
}

// TestNotifierSetWrapper_Stats_Aggregates verifies the set reports the sum of its
// plugins' counters.
func TestNotifierSetWrapper_Stats_Aggregates(t *testing.T) {
	first := &optionalNotifierWrapper{name: "plugin1"}
	first.notifyFailures.Add(2)
	first.restarts.Add(1)

	second := &optionalNotifierWrapper{name: "plugin2"}
	second.notifyFailures.Add(3)
	second.restartFailures.Add(4)

	set := &notifierSetWrapper{notifiers: []*optionalNotifierWrapper{first, second}}

	expected := NotifierStats{NotifyFailures: 5, Restarts: 1, RestartFailures: 4}
	if got := set.Stats(); got != expected {
		t.Errorf("expected %+v, got %+v", expected, got)
	}
}

// buildTestNotifierPlugin compiles the testdata notifier plugin and returns the
// path to the binary.
func buildTestNotifierPlugin(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available; skipping plugin subprocess test")
	}

	name := "notifier_plugin"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}

	path := filepath.Join(t.TempDir(), name)
	output, err := exec.Command("go", "build", "-o", path, "./testdata/notifier_plugin").
		CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build test plugin: %v\n%s", err, output)
	}

	return path
}

// waitFor polls cond until it returns true or the timeout elapses.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) bool {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}

	return cond()
}

// loadTestNotifier builds and loads the testdata plugin, returning the wrapper,
// the single loaded plugin wrapper and the path of the file the plugin appends
// delivered notifications to.
func loadTestNotifier(t *testing.T) (NotifierWrapper, *optionalNotifierWrapper, string) {
	t.Helper()

	executablePath := buildTestNotifierPlugin(t)
	notifyLog := filepath.Join(t.TempDir(), "notifications.log")
	t.Setenv("AGENT_SMITH_TEST_NOTIFY_LOG", notifyLog)

	logBuf := &syncBuffer{}
	wrapper, err := LoadNotifer(
		[]agent.Plugin{{Name: "test-plugin", ExecutablePath: executablePath}},
		logBuf,
		newBufferedLogger(logBuf),
	)
	if err != nil {
		t.Fatalf("failed to load test plugin: %v\n%s", err, logBuf.String())
	}
	t.Cleanup(wrapper.Kill)

	set, ok := wrapper.(*notifierSetWrapper)
	if !ok || len(set.notifiers) != 1 {
		t.Fatalf("expected exactly 1 loaded plugin, got %v", wrapper.Plugins())
	}

	return wrapper, set.notifiers[0], notifyLog
}

// killPlugin kills the plugin subprocess and waits until go-plugin observes the
// exit, mimicking a plugin that crashes mid-run.
func killPlugin(t *testing.T, notifier *optionalNotifierWrapper) {
	t.Helper()

	notifier.mu.Lock()
	reattach := notifier.client.ReattachConfig()
	notifier.mu.Unlock()

	if reattach == nil || reattach.Pid == 0 {
		t.Fatal("expected a running plugin subprocess with a pid")
	}

	process, err := os.FindProcess(reattach.Pid)
	if err != nil {
		t.Fatalf("failed to find plugin process %d: %v", reattach.Pid, err)
	}
	if err := process.Kill(); err != nil {
		t.Fatalf("failed to kill plugin process %d: %v", reattach.Pid, err)
	}

	exited := waitFor(t, 10*time.Second, func() bool {
		notifier.mu.Lock()
		defer notifier.mu.Unlock()
		return notifier.client != nil && notifier.client.Exited()
	})
	if !exited {
		t.Fatal("plugin subprocess exit was never observed")
	}
}

// deliveredNotifications returns the notifications the plugin subprocess has
// written to its log file.
func deliveredNotifications(t *testing.T, notifyLog string) []string {
	t.Helper()

	contents, err := os.ReadFile(notifyLog)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("failed to read notification log: %v", err)
	}

	var messages []string
	for _, line := range strings.Split(string(contents), "\n") {
		if line != "" {
			messages = append(messages, line)
		}
	}

	return messages
}

// TestLoadNotifer_HealthCheckRestartsCrashedPlugin is the end-to-end regression
// test for the silent-notification-loss bug: a plugin subprocess that is killed
// mid-run is detected by the health check, relaunched, and keeps delivering
// notifications without the agent being restarted.
func TestLoadNotifer_HealthCheckRestartsCrashedPlugin(t *testing.T) {
	wrapper, notifier, notifyLog := loadTestNotifier(t)

	if err := wrapper.Notify("AgentStatus:Online"); err != nil {
		t.Fatalf("expected the first notification to be delivered, got %v", err)
	}

	killPlugin(t, notifier)

	wrapper.CheckHealth()

	if got := wrapper.Stats().Restarts; got != 1 {
		t.Fatalf("expected the crashed plugin to be restarted once, got %d", got)
	}

	if err := wrapper.Notify("AgentStatus:Reconnecting"); err != nil {
		t.Fatalf("expected notifications to resume after the restart, got %v", err)
	}

	expected := []string{"AgentStatus:Online", "AgentStatus:Reconnecting"}
	delivered := waitFor(t, 5*time.Second, func() bool {
		return len(deliveredNotifications(t, notifyLog)) == len(expected)
	})
	if !delivered {
		t.Fatalf(
			"expected %v to be delivered, got %v",
			expected,
			deliveredNotifications(t, notifyLog),
		)
	}

	for i, message := range deliveredNotifications(t, notifyLog) {
		if message != expected[i] {
			t.Errorf("notification %d: expected %q, got %q", i, expected[i], message)
		}
	}
}

// TestLoadNotifer_NotifyRestartsCrashedPlugin verifies the relaunch also happens
// on the notification path, so a notification arriving between the crash and the
// next health check is still delivered.
func TestLoadNotifer_NotifyRestartsCrashedPlugin(t *testing.T) {
	wrapper, notifier, notifyLog := loadTestNotifier(t)

	killPlugin(t, notifier)

	if err := wrapper.Notify("AgentStatus:Offline"); err != nil {
		t.Fatalf("expected Notify to relaunch the plugin and deliver, got %v", err)
	}

	stats := wrapper.Stats()
	if stats.Restarts != 1 {
		t.Errorf("expected 1 restart, got %d", stats.Restarts)
	}
	if stats.NotifyFailures != 0 {
		t.Errorf("expected no missed notifications, got %d", stats.NotifyFailures)
	}

	delivered := waitFor(t, 5*time.Second, func() bool {
		return len(deliveredNotifications(t, notifyLog)) == 1
	})
	if !delivered {
		t.Fatalf(
			"expected the notification to be delivered by the relaunched plugin, got %v",
			deliveredNotifications(t, notifyLog),
		)
	}
}

// TestLoadNotifer_NotifyRecoversFromHungPlugin is the end-to-end regression test
// for the "hung, not exited" failure mode: a real plugin subprocess that accepts
// the Notify call and then blocks forever without exiting must not block the
// calling worker past shared.NotifyTimeout, and the still-alive subprocess must
// be killed and relaunched rather than left running and permanently wedged —
// which is exactly what the existing crash-only health check could not detect.
func TestLoadNotifer_NotifyRecoversFromHungPlugin(t *testing.T) {
	original := shared.NotifyTimeout
	shared.NotifyTimeout = 200 * time.Millisecond
	defer func() { shared.NotifyTimeout = original }()

	wrapper, _, notifyLog := loadTestNotifier(t)

	start := time.Now()
	err := wrapper.Notify("HANG_FOREVER")
	elapsed := time.Since(start)

	if !errors.Is(err, shared.ErrNotifyTimeout) {
		t.Fatalf("expected ErrNotifyTimeout from a hung plugin, got %v", err)
	}
	// Bounded by NotifyTimeout plus go-plugin's own 2s graceful-kill grace period;
	// 5x that margin comfortably rules out "actually blocked forever".
	if elapsed > 5*time.Second {
		t.Errorf("Notify was not bounded by the timeout: took %v", elapsed)
	}

	if got := wrapper.Stats().NotifyTimeouts; got != 1 {
		t.Errorf("expected 1 notify timeout counted, got %d", got)
	}

	// The hung subprocess was killed as part of dropping the handle; the next
	// Notify must relaunch it and resume delivery rather than calling a dead
	// client forever.
	if err := wrapper.Notify("AgentStatus:Online"); err != nil {
		t.Fatalf("expected the relaunched plugin to deliver, got %v", err)
	}

	if got := wrapper.Stats().Restarts; got != 1 {
		t.Errorf("expected the hung plugin to be restarted once, got %d", got)
	}

	delivered := waitFor(t, 5*time.Second, func() bool {
		return len(deliveredNotifications(t, notifyLog)) == 1
	})
	if !delivered {
		t.Fatalf(
			"expected the notification to be delivered by the relaunched plugin, got %v",
			deliveredNotifications(t, notifyLog),
		)
	}
	if got := deliveredNotifications(
		t,
		notifyLog,
	); len(got) != 1 ||
		got[0] != "AgentStatus:Online" {
		t.Errorf("expected [AgentStatus:Online] delivered, got %v", got)
	}
}

// TestLoadNotifer_HealthyPluginIsNotRestarted verifies the supervision is inert
// while the plugin stays healthy.
func TestLoadNotifer_HealthyPluginIsNotRestarted(t *testing.T) {
	wrapper, _, notifyLog := loadTestNotifier(t)

	for range 3 {
		wrapper.CheckHealth()
		if err := wrapper.Notify("AgentStatus:Online"); err != nil {
			t.Fatalf("expected the notification to be delivered, got %v", err)
		}
	}

	if stats := wrapper.Stats(); stats != (NotifierStats{}) {
		t.Errorf("expected no failures or restarts for a healthy plugin, got %+v", stats)
	}

	delivered := waitFor(t, 5*time.Second, func() bool {
		return len(deliveredNotifications(t, notifyLog)) == 3
	})
	if !delivered {
		t.Errorf(
			"expected 3 notifications delivered, got %v",
			deliveredNotifications(t, notifyLog),
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
