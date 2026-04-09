//go:build darwin

package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mockLaunchCtl

type mockLaunchCtl struct {
	runCalls    [][]string
	runOutput   []byte
	runErr      error
	runErrOnCmd string // if set, only fail when args[0] matches
	plistPath   string
}

func (m *mockLaunchCtl) Run(args ...string) ([]byte, error) {
	m.runCalls = append(m.runCalls, args)
	if m.runErrOnCmd != "" {
		if len(args) > 0 && args[0] == m.runErrOnCmd {
			return nil, m.runErr
		}
		return m.runOutput, nil
	}
	if m.runErr != nil {
		return nil, m.runErr
	}
	return m.runOutput, nil
}

func (m *mockLaunchCtl) PlistFilePath(name string) string {
	return m.plistPath
}

// helpers

func newTempPlistPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test.plist")
}

// darwinService tests

func TestDarwinService_Close(t *testing.T) {
	svc := &darwinService{name: "test-svc", system: &mockLaunchCtl{}}
	if err := svc.Close(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestDarwinService_Start(t *testing.T) {
	plist := newTempPlistPath(t)
	mock := &mockLaunchCtl{plistPath: plist}
	svc := &darwinService{name: "test-svc", system: mock}

	if err := svc.Start(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(mock.runCalls) != 2 {
		t.Fatalf("expected 2 Run calls, got %d", len(mock.runCalls))
	}
	if mock.runCalls[0][0] != "load" || mock.runCalls[0][1] != plist {
		t.Errorf("expected Run(load, %s), got %v", plist, mock.runCalls[0])
	}
	if mock.runCalls[1][0] != "start" || mock.runCalls[1][1] != "test-svc" {
		t.Errorf("expected Run(start, test-svc), got %v", mock.runCalls[1])
	}
}

func TestDarwinService_Start_LoadError(t *testing.T) {
	mock := &mockLaunchCtl{
		plistPath:   newTempPlistPath(t),
		runErrOnCmd: "load",
		runErr:      errors.New("load failed"),
	}
	svc := &darwinService{name: "test-svc", system: mock}

	if err := svc.Start(); err == nil {
		t.Error("expected error, got nil")
	}
	if len(mock.runCalls) != 1 {
		t.Errorf(
			"expected start not to be called after load failure, got %d calls",
			len(mock.runCalls),
		)
	}
}

func TestDarwinService_Start_StartError(t *testing.T) {
	mock := &mockLaunchCtl{
		plistPath:   newTempPlistPath(t),
		runErrOnCmd: "start",
		runErr:      errors.New("start failed"),
	}
	svc := &darwinService{name: "test-svc", system: mock}

	if err := svc.Start(); err == nil {
		t.Error("expected error, got nil")
	}
}

func TestDarwinService_Stop(t *testing.T) {
	plist := newTempPlistPath(t)
	mock := &mockLaunchCtl{plistPath: plist}
	svc := &darwinService{name: "test-svc", system: mock}

	if err := svc.Stop(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(mock.runCalls) != 2 {
		t.Fatalf("expected 2 Run calls, got %d", len(mock.runCalls))
	}
	if mock.runCalls[0][0] != "stop" || mock.runCalls[0][1] != "test-svc" {
		t.Errorf("expected Run(stop, test-svc), got %v", mock.runCalls[0])
	}
	if mock.runCalls[1][0] != "unload" || mock.runCalls[1][1] != plist {
		t.Errorf("expected Run(unload, %s), got %v", plist, mock.runCalls[1])
	}
}

func TestDarwinService_Stop_StopError(t *testing.T) {
	mock := &mockLaunchCtl{
		plistPath:   newTempPlistPath(t),
		runErrOnCmd: "stop",
		runErr:      errors.New("stop failed"),
	}
	svc := &darwinService{name: "test-svc", system: mock}

	if err := svc.Stop(); err == nil {
		t.Error("expected error, got nil")
	}
	if len(mock.runCalls) != 1 {
		t.Errorf(
			"expected unload not to be called after stop failure, got %d calls",
			len(mock.runCalls),
		)
	}
}

func TestDarwinService_Stop_UnloadError(t *testing.T) {
	mock := &mockLaunchCtl{
		plistPath:   newTempPlistPath(t),
		runErrOnCmd: "unload",
		runErr:      errors.New("unload failed"),
	}
	svc := &darwinService{name: "test-svc", system: mock}

	if err := svc.Stop(); err == nil {
		t.Error("expected error, got nil")
	}
}

func TestDarwinService_Delete(t *testing.T) {
	tmpFile := newTempPlistPath(t)
	if err := os.WriteFile(tmpFile, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	mock := &mockLaunchCtl{plistPath: tmpFile}
	svc := &darwinService{name: "test-svc", system: mock}

	if err := svc.Delete(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(mock.runCalls) != 1 || mock.runCalls[0][0] != "unload" ||
		mock.runCalls[0][1] != "test-svc" {
		t.Errorf("expected Run(unload, test-svc), got %v", mock.runCalls)
	}
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Error("expected plist file to be removed")
	}
}

func TestDarwinService_Delete_UnloadError(t *testing.T) {
	mock := &mockLaunchCtl{
		plistPath: newTempPlistPath(t),
		runErr:    errors.New("unload failed"),
	}
	svc := &darwinService{name: "test-svc", system: mock}

	if err := svc.Delete(); err == nil {
		t.Error("expected error, got nil")
	}
}

func TestDarwinService_Delete_RemoveError(t *testing.T) {
	mock := &mockLaunchCtl{plistPath: "/nonexistent/path/file.plist"}
	svc := &darwinService{name: "test-svc", system: mock}

	if err := svc.Delete(); err == nil {
		t.Error("expected error removing nonexistent file, got nil")
	}
}

func TestDarwinService_IsActive_Running(t *testing.T) {
	mock := &mockLaunchCtl{
		runOutput: []byte("{\n\tstate = running\n\tpid = 1234\n}"),
	}
	svc := &darwinService{name: "test-svc", system: mock}

	if !svc.IsActive() {
		t.Error("expected IsActive to return true when state is running")
	}
}

func TestDarwinService_IsActive_Stopped(t *testing.T) {
	mock := &mockLaunchCtl{
		runOutput: []byte("{\n\tstate = stopped\n}"),
	}
	svc := &darwinService{name: "test-svc", system: mock}

	if svc.IsActive() {
		t.Error("expected IsActive to return false when state is stopped")
	}
}

func TestDarwinService_IsActive_Error(t *testing.T) {
	mock := &mockLaunchCtl{runErr: errors.New("not found")}
	svc := &darwinService{name: "test-svc", system: mock}

	if svc.IsActive() {
		t.Error("expected IsActive to return false on error")
	}
}

func TestDarwinService_IsActive_NoStateLine(t *testing.T) {
	mock := &mockLaunchCtl{
		runOutput: []byte("{\n\tpid = 1234\n}"),
	}
	svc := &darwinService{name: "test-svc", system: mock}

	if svc.IsActive() {
		t.Error("expected IsActive to return false when state line is absent")
	}
}

// NewServiceManager tests

func TestNewServiceManager_ReturnsNonNil(t *testing.T) {
	sm := NewServiceManager()
	if sm == nil {
		t.Fatal("expected non-nil ServiceManager")
	}
}

// defaultServiceManager.Create tests

func TestDefaultServiceManager_Create_Success(t *testing.T) {
	tmpFile := newTempPlistPath(t)
	mock := &mockLaunchCtl{plistPath: tmpFile}
	sm := &defaultServiceManager{system: mock}
	params := AgentParams{
		Name:                "test-svc",
		AgentExecutablePath: "/usr/bin/agent",
		OrgId:               "org-123",
		ConfigFilePath:      "/etc/agent/config.json",
		LogFilePath:         "/var/log/agent.log",
	}

	svc, err := sm.Create(params)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if svc == nil {
		t.Fatal("expected service, got nil")
	}
}

func TestDefaultServiceManager_Create_PlistContent(t *testing.T) {
	tmpFile := newTempPlistPath(t)
	mock := &mockLaunchCtl{plistPath: tmpFile}
	sm := &defaultServiceManager{system: mock}
	params := AgentParams{
		Name:                "my-service",
		AgentExecutablePath: "/usr/bin/agent",
		OrgId:               "org-abc",
		ConfigFilePath:      "/etc/agent.json",
		LogFilePath:         "/var/log/agent.log",
	}

	if _, err := sm.Create(params); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("failed to read plist file: %v", err)
	}
	content := string(data)

	checks := []string{
		"<?xml version=\"1.0\" encoding=\"UTF-8\"?>",
		"<key>Label</key>",
		"<string>my-service</string>",
		"<string>/usr/bin/agent</string>",
		"<string>--org-id</string>",
		"<string>org-abc</string>",
		"<string>--config-file</string>",
		"<string>/etc/agent.json</string>",
		"<string>--log-file</string>",
		"<string>/var/log/agent.log</string>",
		"<key>RunAtLoad</key>",
		"<key>KeepAlive</key>",
		"<key>EnvironmentVariables</key>",
		"/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("expected plist to contain %q, got:\n%s", check, content)
		}
	}
}

func TestDefaultServiceManager_Create_WriteFileError(t *testing.T) {
	mock := &mockLaunchCtl{plistPath: "/nonexistent/dir/file.plist"}
	sm := &defaultServiceManager{system: mock}

	_, err := sm.Create(AgentParams{Name: "test-svc"})

	if err == nil {
		t.Error("expected error on WriteFile failure, got nil")
	}
}

// defaultServiceManager.Open tests

func TestDefaultServiceManager_Open_Success(t *testing.T) {
	mock := &mockLaunchCtl{}
	sm := &defaultServiceManager{system: mock}

	svc, err := sm.Open("test-svc")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if svc == nil {
		t.Fatal("expected service, got nil")
	}
	if len(mock.runCalls) != 1 || mock.runCalls[0][0] != "print" ||
		mock.runCalls[0][1] != "system/test-svc" {
		t.Errorf("expected Run(print, system/test-svc), got %v", mock.runCalls)
	}
}

func TestDefaultServiceManager_Open_Error(t *testing.T) {
	mock := &mockLaunchCtl{runErr: errors.New("not found")}
	sm := &defaultServiceManager{system: mock}

	_, err := sm.Open("test-svc")

	if err == nil {
		t.Error("expected error, got nil")
	}
}
