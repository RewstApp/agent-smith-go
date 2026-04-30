//go:build windows

package syslog

import (
	"bytes"
	"errors"
	"io"
	"syscall"
	"testing"

	"golang.org/x/sys/windows/registry"
)

// mockEventLogger

type mockEventLogger struct {
	infoMessages    []string
	warningMessages []string
	errorMessages   []string
	closed          bool
}

func (m *mockEventLogger) Info(eid uint32, msg string) error {
	m.infoMessages = append(m.infoMessages, msg)
	return nil
}

func (m *mockEventLogger) Warning(eid uint32, msg string) error {
	m.warningMessages = append(m.warningMessages, msg)
	return nil
}

func (m *mockEventLogger) Error(eid uint32, msg string) error {
	m.errorMessages = append(m.errorMessages, msg)
	return nil
}

func (m *mockEventLogger) Close() error {
	m.closed = true
	return nil
}

// mockEventLogFactory

type mockEventLogFactory struct {
	openKeyErr    error
	installErr    error
	openErr       error
	openResult    eventLogger
	installCalled bool
}

func (m *mockEventLogFactory) OpenKey(name string) (io.Closer, error) {
	if m.openKeyErr != nil {
		return nil, m.openKeyErr
	}
	return io.NopCloser(nil), nil
}

func (m *mockEventLogFactory) Install(name string) error {
	m.installCalled = true
	return m.installErr
}

func (m *mockEventLogFactory) Open(name string) (eventLogger, error) {
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

func TestNewWithFactory_OpenKeySuccess_SkipsInstall(t *testing.T) {
	logger := &mockEventLogger{}
	factory := &mockEventLogFactory{
		openResult: logger,
	}

	syslogger, err := newWithFactory("test", &bytes.Buffer{}, factory)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if syslogger == nil {
		t.Fatal("expected non-nil Syslog")
	}
	if factory.installCalled {
		t.Error("expected Install not to be called when OpenKey succeeds")
	}
}

func TestNewWithFactory_OpenKeyErrNotExist_CallsInstall(t *testing.T) {
	logger := &mockEventLogger{}
	factory := &mockEventLogFactory{
		openKeyErr: registry.ErrNotExist,
		openResult: logger,
	}

	syslogger, err := newWithFactory("test", &bytes.Buffer{}, factory)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if syslogger == nil {
		t.Fatal("expected non-nil Syslog")
	}
	if !factory.installCalled {
		t.Error("expected Install to be called when OpenKey returns ErrNotExist")
	}
}

func TestNewWithFactory_OpenKeyErrPathNotFound_CallsInstall(t *testing.T) {
	logger := &mockEventLogger{}
	factory := &mockEventLogFactory{
		openKeyErr: syscall.ERROR_PATH_NOT_FOUND,
		openResult: logger,
	}

	syslogger, err := newWithFactory("test", &bytes.Buffer{}, factory)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if syslogger == nil {
		t.Fatal("expected non-nil Syslog")
	}
	if !factory.installCalled {
		t.Error("expected Install to be called when OpenKey returns ERROR_PATH_NOT_FOUND")
	}
}

func TestNewWithFactory_OpenKeyUnknownError_ReturnsError(t *testing.T) {
	factory := &mockEventLogFactory{
		openKeyErr: errors.New("unexpected registry error"),
	}

	_, err := newWithFactory("test", &bytes.Buffer{}, factory)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "unexpected registry error" {
		t.Errorf("expected 'unexpected registry error', got %q", err.Error())
	}
	if factory.installCalled {
		t.Error("expected Install not to be called on unknown OpenKey error")
	}
}

func TestNewWithFactory_InstallError_ProceedsToOpen(t *testing.T) {
	logger := &mockEventLogger{}
	factory := &mockEventLogFactory{
		openKeyErr: registry.ErrNotExist,
		installErr: errors.New("access denied"),
		openResult: logger,
	}

	syslogger, err := newWithFactory("test", &bytes.Buffer{}, factory)
	if err != nil {
		t.Fatalf("expected no error when install fails, got %v", err)
	}
	if syslogger == nil {
		t.Fatal("expected non-nil Syslog even when install fails")
	}
	if !factory.installCalled {
		t.Error("expected Install to be attempted")
	}
}

func TestEnsureSourceWithFactory_KeyExists_SkipsInstall(t *testing.T) {
	factory := &mockEventLogFactory{}

	err := ensureSourceWithFactory("test", factory)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if factory.installCalled {
		t.Error("expected Install not to be called when key already exists")
	}
}

func TestEnsureSourceWithFactory_KeyNotExist_Installs(t *testing.T) {
	factory := &mockEventLogFactory{
		openKeyErr: registry.ErrNotExist,
	}

	err := ensureSourceWithFactory("test", factory)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !factory.installCalled {
		t.Error("expected Install to be called when key does not exist")
	}
}

func TestEnsureSourceWithFactory_InstallError_ReturnsError(t *testing.T) {
	factory := &mockEventLogFactory{
		openKeyErr: registry.ErrNotExist,
		installErr: errors.New("install failed"),
	}

	err := ensureSourceWithFactory("test", factory)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestEnsureSourceWithFactory_OtherKeyError_ReturnsError(t *testing.T) {
	factory := &mockEventLogFactory{
		openKeyErr: errors.New("unexpected registry error"),
	}

	err := ensureSourceWithFactory("test", factory)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestNewWithFactory_OpenError_ReturnsError(t *testing.T) {
	factory := &mockEventLogFactory{
		openErr: errors.New("open failed"),
	}

	_, err := newWithFactory("test", &bytes.Buffer{}, factory)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "open failed" {
		t.Errorf("expected 'open failed', got %q", err.Error())
	}
}

func TestNewWithFactory_ReturnedSyslog_IsUsable(t *testing.T) {
	logger := &mockEventLogger{}
	var out bytes.Buffer
	factory := &mockEventLogFactory{
		openResult: logger,
	}

	syslogger, err := newWithFactory("test", &out, factory)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	data := []byte("[ERROR] test error")

	_, err = syslogger.Write(data)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if len(logger.errorMessages) != 1 {
		t.Fatalf("expected 1 error message, got %d", len(logger.errorMessages))
	}

	if out.String() != string(data) {
		t.Errorf("expected out %q, got %q", string(data), out.String())
	}
}
