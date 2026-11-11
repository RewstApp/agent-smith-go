//go:build windows

package syslog

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/windows/registry"
)

// mockEventLogger

type mockEventLogger struct {
	infoMessages    []string
	warningMessages []string
	errorMessages   []string
	closed          bool

	// reportErr is returned by every report call when set.
	reportErr error
}

func (m *mockEventLogger) Info(eid uint32, msg string) error {
	m.infoMessages = append(m.infoMessages, msg)
	return m.reportErr
}

func (m *mockEventLogger) Warning(eid uint32, msg string) error {
	m.warningMessages = append(m.warningMessages, msg)
	return m.reportErr
}

func (m *mockEventLogger) Error(eid uint32, msg string) error {
	m.errorMessages = append(m.errorMessages, msg)
	return m.reportErr
}

func (m *mockEventLogger) Close() error {
	m.closed = true
	return nil
}

// mockEventLogFactory

type mockEventLogFactory struct {
	openKeyErr error
	// openKeyErrOnce applies openKeyErr to the first OpenKey call only, so a
	// test can play the key appearing between the read and the install.
	openKeyErrOnce bool
	installErr     error
	openErr        error
	openResult     eventLogger

	openKeyCalls  int
	installCalls  int
	openCalls     int
	installCalled bool
}

func (m *mockEventLogFactory) OpenKey(name string) (io.Closer, error) {
	m.openKeyCalls++
	if m.openKeyErr != nil && (!m.openKeyErrOnce || m.openKeyCalls == 1) {
		return nil, m.openKeyErr
	}
	return io.NopCloser(nil), nil
}

func (m *mockEventLogFactory) Install(name string) error {
	m.installCalls++
	m.installCalled = true
	return m.installErr
}

func (m *mockEventLogFactory) Open(name string) (eventLogger, error) {
	m.openCalls++
	return m.openResult, m.openErr
}

// windowsSyslog.Write tests

func TestWindowsSyslog_Write_Info(t *testing.T) {
	mock := &mockEventLogger{}
	var out bytes.Buffer
	s := &windowsSyslog{out: &out, log: mock}

	data := []byte("[INFO] hello world")
	n, err := s.Write(data)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if n != len(data) {
		t.Errorf("expected %d bytes written, got %d", len(data), n)
	}
	if len(mock.infoMessages) != 1 {
		t.Fatalf("expected 1 info message, got %d", len(mock.infoMessages))
	}
	if mock.infoMessages[0] != "hello world" {
		t.Errorf("expected message 'hello world', got %q", mock.infoMessages[0])
	}
	if len(mock.warningMessages) != 0 || len(mock.errorMessages) != 0 {
		t.Error("expected no warning or error messages")
	}
}

func TestWindowsSyslog_Write_Warning(t *testing.T) {
	mock := &mockEventLogger{}
	var out bytes.Buffer
	s := &windowsSyslog{out: &out, log: mock}

	data := []byte("[WARNING] disk space low")
	_, err := s.Write(data)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(mock.warningMessages) != 1 {
		t.Fatalf("expected 1 warning message, got %d", len(mock.warningMessages))
	}
	if mock.warningMessages[0] != "disk space low" {
		t.Errorf("expected message 'disk space low', got %q", mock.warningMessages[0])
	}
	if len(mock.infoMessages) != 0 || len(mock.errorMessages) != 0 {
		t.Error("expected no info or error messages")
	}
}

// hclog writes "[WARN] ", not "[WARNING]", so this is the spelling that has to
// reach the event log as a warning for real agent warnings.
func TestWindowsSyslog_Write_Warn(t *testing.T) {
	mock := &mockEventLogger{}
	var out bytes.Buffer
	s := &windowsSyslog{out: &out, log: mock}

	data := []byte("2024-01-01T00:00:00.000Z [WARN]  agent_smith: disk space low")
	_, err := s.Write(data)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(mock.warningMessages) != 1 {
		t.Fatalf("expected 1 warning message, got %d", len(mock.warningMessages))
	}
	if len(mock.infoMessages) != 0 || len(mock.errorMessages) != 0 {
		t.Error("expected no info or error messages")
	}
}

func TestWindowsSyslog_Write_Error(t *testing.T) {
	mock := &mockEventLogger{}
	var out bytes.Buffer
	s := &windowsSyslog{out: &out, log: mock}

	data := []byte("[ERROR] something failed")
	_, err := s.Write(data)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(mock.errorMessages) != 1 {
		t.Fatalf("expected 1 error message, got %d", len(mock.errorMessages))
	}
	if mock.errorMessages[0] != "something failed" {
		t.Errorf("expected message 'something failed', got %q", mock.errorMessages[0])
	}
	if len(mock.infoMessages) != 0 || len(mock.warningMessages) != 0 {
		t.Error("expected no info or warning messages")
	}
}

func TestWindowsSyslog_Write_ForwardsToOut(t *testing.T) {
	mock := &mockEventLogger{}
	var out bytes.Buffer
	s := &windowsSyslog{out: &out, log: mock}

	data := []byte("[INFO] forwarded message")

	_, err := s.Write(data)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if out.String() != string(data) {
		t.Errorf("expected out to contain %q, got %q", string(data), out.String())
	}
}

func TestWindowsSyslog_Close(t *testing.T) {
	mock := &mockEventLogger{}
	s := &windowsSyslog{out: &bytes.Buffer{}, log: mock}

	err := s.Close()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !mock.closed {
		t.Error("expected eventLogger.Close to be called")
	}
}

// newWithFactory tests
//
// The event source is opened on the first write, never in the constructor,
// and a failure at any step degrades forwarding instead of failing the
// writer (sc-133002): the service used to exit with LogFileError before it
// had logged a line.

func writeLine(t *testing.T, s Syslog, line string) {
	t.Helper()
	data := []byte(line)
	n, err := s.Write(data)
	if err != nil {
		t.Fatalf("expected no error writing %q, got %v", line, err)
	}
	if n != len(data) {
		t.Fatalf("expected %d bytes written for %q, got %d", len(data), line, n)
	}
}

func TestNewWithFactory_DoesNotTouchTheEventLogUntilTheFirstWrite(t *testing.T) {
	factory := &mockEventLogFactory{openResult: &mockEventLogger{}}

	s := newWithFactory("test", &bytes.Buffer{}, factory)
	if s == nil {
		t.Fatal("expected non-nil Syslog")
	}
	if factory.openKeyCalls != 0 || factory.installCalls != 0 || factory.openCalls != 0 {
		t.Errorf(
			"expected no event-log calls before the first write, got key=%d install=%d open=%d",
			factory.openKeyCalls,
			factory.installCalls,
			factory.openCalls,
		)
	}

	writeLine(t, s, "[INFO] first\n")
	if factory.openKeyCalls != 1 || factory.openCalls != 1 {
		t.Errorf("expected the first write to open the source once, got key=%d open=%d",
			factory.openKeyCalls, factory.openCalls)
	}
}

func TestNewWithFactory_OpenKeySuccess_SkipsInstall(t *testing.T) {
	logger := &mockEventLogger{}
	factory := &mockEventLogFactory{openResult: logger}

	s := newWithFactory("test", &bytes.Buffer{}, factory)
	writeLine(t, s, "[INFO] hello\n")

	if factory.installCalled {
		t.Error("expected Install not to be called when OpenKey succeeds")
	}
	if len(logger.infoMessages) != 1 {
		t.Errorf(
			"expected the line to reach the event log, got %d messages",
			len(logger.infoMessages),
		)
	}
}

func TestNewWithFactory_OpenKeyErrNotExist_CallsInstall(t *testing.T) {
	logger := &mockEventLogger{}
	factory := &mockEventLogFactory{openKeyErr: registry.ErrNotExist, openResult: logger}

	s := newWithFactory("test", &bytes.Buffer{}, factory)
	writeLine(t, s, "[INFO] hello\n")

	if !factory.installCalled {
		t.Error("expected Install to be called when OpenKey returns ErrNotExist")
	}
	if len(logger.infoMessages) != 1 {
		t.Errorf(
			"expected the line to reach the event log, got %d messages",
			len(logger.infoMessages),
		)
	}
}

func TestNewWithFactory_OpenKeyErrPathNotFound_CallsInstall(t *testing.T) {
	factory := &mockEventLogFactory{
		openKeyErr: syscall.ERROR_PATH_NOT_FOUND,
		openResult: &mockEventLogger{},
	}

	s := newWithFactory("test", &bytes.Buffer{}, factory)
	writeLine(t, s, "[INFO] hello\n")

	if !factory.installCalled {
		t.Error("expected Install to be called when OpenKey returns ERROR_PATH_NOT_FOUND")
	}
}

// Registration is opened once and the handle reused: a healthy writer must not
// pay a registry read and a RegisterEventSource per line.
func TestNewWithFactory_ReusesTheOpenHandle(t *testing.T) {
	logger := &mockEventLogger{}
	factory := &mockEventLogFactory{openResult: logger}

	s := newWithFactory("test", &bytes.Buffer{}, factory)
	for range 5 {
		writeLine(t, s, "[INFO] hello\n")
	}

	if factory.openKeyCalls != 1 || factory.openCalls != 1 {
		t.Errorf("expected one registration for five lines, got key=%d open=%d",
			factory.openKeyCalls, factory.openCalls)
	}
	if len(logger.infoMessages) != 5 {
		t.Errorf("expected 5 event-log messages, got %d", len(logger.infoMessages))
	}
}

// The failure the live Windows smoke showed: the source cannot be opened when
// the service starts. The line must still land in the log file, with the
// cause recorded next to it, and the write must report success to hclog.
func TestNewWithFactory_OpenError_FailsOpenToTheFile(t *testing.T) {
	var out bytes.Buffer
	factory := &mockEventLogFactory{openErr: errors.New("RPC server unavailable")}

	s := newWithFactory("rewst_agent_smith_test", &out, factory)
	data := "2026-01-01T00:00:00.000Z [INFO]  agent_smith: Agent Smith started\n"
	writeLine(t, s, data)

	got := out.String()
	if !strings.HasSuffix(got, data) {
		t.Fatalf("expected the log line to reach the file last, got %q", got)
	}
	if !strings.Contains(
		got,
		"[WARN]  rewst_agent_smith_test: event log forwarding failed, suppressing it for 1m0s (log lines still reach this file): register event source rewst_agent_smith_test: RPC server unavailable",
	) {
		t.Errorf("expected the failure recorded in hclog's format before the line, got %q", got)
	}
}

func TestNewWithFactory_OpenKeyUnknownError_FailsOpenToTheFile(t *testing.T) {
	var out bytes.Buffer
	factory := &mockEventLogFactory{
		openKeyErr: errors.New("unexpected registry error"),
		openResult: &mockEventLogger{},
	}

	s := newWithFactory("test", &out, factory)
	writeLine(t, s, "[INFO] hello\n")

	if factory.installCalled {
		t.Error("expected Install not to be called on an unknown OpenKey error")
	}
	if factory.openCalls != 0 {
		t.Error("expected Open not to be called when the key cannot be read")
	}
	if !strings.Contains(out.String(), "read event source key test: unexpected registry error") {
		t.Errorf("expected the registry error recorded in the file, got %q", out.String())
	}
	if !strings.HasSuffix(out.String(), "[INFO] hello\n") {
		t.Errorf("expected the line in the file, got %q", out.String())
	}
}

func TestNewWithFactory_InstallError_FailsOpenToTheFile(t *testing.T) {
	var out bytes.Buffer
	factory := &mockEventLogFactory{
		openKeyErr: registry.ErrNotExist,
		installErr: errors.New("install failed"),
		openResult: &mockEventLogger{},
	}

	s := newWithFactory("test", &out, factory)
	writeLine(t, s, "[INFO] hello\n")

	if factory.openCalls != 0 {
		t.Error(
			"expected Open not to be called when the install failed and the key is still missing",
		)
	}
	if !strings.Contains(out.String(), "install event source test: install failed") {
		t.Errorf("expected the install error recorded in the file, got %q", out.String())
	}
	if !strings.HasSuffix(out.String(), "[INFO] hello\n") {
		t.Errorf("expected the line in the file, got %q", out.String())
	}
}

// InstallAsEventCreate refuses a key that already exists. When the key
// appears between the read and the install (another agent process registering
// the same service name), the source is registered and the open proceeds.
func TestNewWithFactory_InstallRefusedButKeyNowExists_Opens(t *testing.T) {
	logger := &mockEventLogger{}
	var out bytes.Buffer
	factory := &mockEventLogFactory{
		openKeyErr:     registry.ErrNotExist,
		openKeyErrOnce: true,
		installErr:     errors.New("registry key already exists"),
		openResult:     logger,
	}

	s := newWithFactory("test", &out, factory)
	writeLine(t, s, "[INFO] hello\n")

	if factory.openKeyCalls != 2 {
		t.Errorf(
			"expected the key re-read after the refused install, got %d reads",
			factory.openKeyCalls,
		)
	}
	if factory.openCalls != 1 {
		t.Errorf("expected the source opened once the key exists, got %d opens", factory.openCalls)
	}
	if len(logger.infoMessages) != 1 {
		t.Errorf(
			"expected the line to reach the event log, got %d messages",
			len(logger.infoMessages),
		)
	}
	if strings.Contains(out.String(), "event log forwarding failed") {
		t.Errorf("expected no failure recorded, got %q", out.String())
	}
}

// Once registration fails, the agent stops paying for it on every line until
// the suppression window elapses, reports the failure once, and keeps every
// line in the file.
func TestWindowsSyslog_Write_SuppressesRegistrationAfterFailure(t *testing.T) {
	var out bytes.Buffer
	factory := &mockEventLogFactory{openErr: errors.New("open failed")}
	s := &windowsSyslog{out: &out, source: "test", factory: factory, suppressWindow: time.Hour}

	for range 5 {
		writeLine(t, s, "[INFO] message\n")
	}

	if factory.openCalls != 1 {
		t.Errorf(
			"expected only the first line to attempt registration, got %d opens",
			factory.openCalls,
		)
	}
	if got := strings.Count(out.String(), "event log forwarding failed"); got != 1 {
		t.Errorf("expected the failure reported once, got %d times", got)
	}
	if got := strings.Count(out.String(), "[INFO] message"); got != 5 {
		t.Errorf("expected all 5 lines in the on-disk log, got %d", got)
	}
}

func TestWindowsSyslog_Write_ProbesRegistrationAndReportsRecovery(t *testing.T) {
	logger := &mockEventLogger{}
	var out bytes.Buffer
	factory := &mockEventLogFactory{openErr: errors.New("open failed"), openResult: logger}
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := &windowsSyslog{
		out:     &out,
		source:  "test",
		factory: factory,
		health:  forwardHealth{now: clock.now},
	}

	writeLine(t, s, "[INFO] first\n")

	// Still inside the window: no probe.
	clock.t = clock.t.Add(syslogSuppressWindow / 2)
	writeLine(t, s, "[INFO] between\n")
	if factory.openCalls != 1 {
		t.Fatalf(
			"expected no registration attempt inside the window, got %d opens",
			factory.openCalls,
		)
	}

	factory.openErr = nil
	clock.t = clock.t.Add(syslogSuppressWindow)
	writeLine(t, s, "[INFO] second\n")

	if factory.openCalls != 2 {
		t.Errorf(
			"expected the elapsed window to allow a registration probe, got %d opens",
			factory.openCalls,
		)
	}
	if len(logger.infoMessages) != 1 || logger.infoMessages[0] != "second" {
		t.Errorf("expected the probe line in the event log, got %v", logger.infoMessages)
	}
	if !strings.Contains(
		out.String(),
		"event log forwarding recovered after 1 failure(s); 1 line(s) reached this log only",
	) {
		t.Errorf("expected a recovery diagnostic, got %q", out.String())
	}

	// Recovery is reported once, not on every subsequent healthy line, and
	// the recovered handle is reused.
	writeLine(t, s, "[INFO] third\n")
	if got := strings.Count(out.String(), "event log forwarding recovered"); got != 1 {
		t.Errorf("expected recovery reported once, got %d times", got)
	}
	if factory.openCalls != 2 {
		t.Errorf("expected the recovered handle reused, got %d opens", factory.openCalls)
	}
}

// A report that fails through an open handle (the EventLog service restarted
// under it) is a forwarding failure like any other: recorded once, suppressed,
// and the handle is dropped so the probe re-registers from the start.
func TestWindowsSyslog_Write_ReportErrorDropsTheHandleAndReregisters(t *testing.T) {
	stale := &mockEventLogger{reportErr: errors.New("invalid handle")}
	fresh := &mockEventLogger{}
	var out bytes.Buffer
	factory := &mockEventLogFactory{openResult: fresh}
	clock := &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := &windowsSyslog{
		out:     &out,
		source:  "test",
		factory: factory,
		log:     stale,
		health:  forwardHealth{now: clock.now},
	}

	writeLine(t, s, "[ERROR] first\n")

	if !stale.closed {
		t.Error("expected the failing handle to be closed")
	}
	if !strings.Contains(
		out.String(),
		"event log forwarding failed, suppressing it for 1m0s (log lines still reach this file): report event as test: invalid handle",
	) {
		t.Errorf("expected the report error recorded, got %q", out.String())
	}

	clock.t = clock.t.Add(2 * syslogSuppressWindow)
	writeLine(t, s, "[ERROR] second\n")

	if factory.openCalls != 1 {
		t.Errorf("expected the probe to re-register the source, got %d opens", factory.openCalls)
	}
	if len(fresh.errorMessages) != 1 || fresh.errorMessages[0] != "second" {
		t.Errorf("expected the probe line through the fresh handle, got %v", fresh.errorMessages)
	}
	if !strings.Contains(out.String(), "event log forwarding recovered") {
		t.Errorf("expected a recovery diagnostic, got %q", out.String())
	}
}

// A file write error still surfaces: the event log is best effort, the file
// is not.
func TestWindowsSyslog_Write_ReturnsTheFileWriteError(t *testing.T) {
	outErr := errors.New("disk full")
	s := &windowsSyslog{out: &failingWriter{err: outErr}, source: "test", log: &mockEventLogger{}}

	n, err := s.Write([]byte("[INFO] hello\n"))
	if !errors.Is(err, outErr) {
		t.Fatalf("expected the file write error, got %v", err)
	}
	if n != 0 {
		t.Errorf("expected 0 bytes written, got %d", n)
	}
}

type failingWriter struct {
	err error
}

// fakeClock stands in for time.Now so the suppression window elapses exactly
// when a test says it does.
type fakeClock struct {
	t time.Time
}

func (c *fakeClock) now() time.Time {
	return c.t
}

func (w *failingWriter) Write(p []byte) (int, error) {
	return 0, w.err
}

func TestWindowsSyslog_Close_BeforeTheFirstWrite(t *testing.T) {
	factory := &mockEventLogFactory{openResult: &mockEventLogger{}}
	s := newWithFactory("test", &bytes.Buffer{}, factory)

	if err := s.Close(); err != nil {
		t.Fatalf("expected no error closing an unopened writer, got %v", err)
	}
	if factory.openCalls != 0 {
		t.Error("expected Close not to open the source")
	}
}

func TestNewWithFactory_ReturnedSyslog_IsUsable(t *testing.T) {
	logger := &mockEventLogger{}
	var out bytes.Buffer
	factory := &mockEventLogFactory{openResult: logger}

	syslogger := newWithFactory("test", &out, factory)

	data := []byte("[ERROR] test error")
	if _, err := syslogger.Write(data); err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if len(logger.errorMessages) != 1 {
		t.Fatalf("expected 1 error message, got %d", len(logger.errorMessages))
	}
	if out.String() != string(data) {
		t.Errorf("expected out %q, got %q", string(data), out.String())
	}
}
