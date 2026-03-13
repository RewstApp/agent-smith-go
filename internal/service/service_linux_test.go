//go:build linux

package service

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mockSystemCtl

type mockSystemCtl struct {
	runCalls       [][]string
	runErrOnCmd    string // if set, only fail when args[0] matches
	runErr         error
	configFilePath string
}

func (m *mockSystemCtl) Run(args ...string) error {
	m.runCalls = append(m.runCalls, args)
	if m.runErrOnCmd != "" {
		if len(args) > 0 && args[0] == m.runErrOnCmd {
			return m.runErr
		}
		return nil
	}
	return m.runErr
}

func (m *mockSystemCtl) ServiceConfigFilePath(name string) string {
	return m.configFilePath
}

// helpers

func newTempConfigPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test.service")
}

// linuxService tests

func TestLinuxService_Close(t *testing.T) {
	svc := &linuxService{name: "test-svc", system: &mockSystemCtl{}}
	if err := svc.Close(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestLinuxService_Start(t *testing.T) {
	mock := &mockSystemCtl{}
	svc := &linuxService{name: "test-svc", system: mock}

	if err := svc.Start(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(mock.runCalls) != 1 || mock.runCalls[0][0] != "start" ||
		mock.runCalls[0][1] != "test-svc" {
		t.Errorf("expected Run(start, test-svc), got %v", mock.runCalls)
	}
}

func TestLinuxService_Start_Error(t *testing.T) {
	mock := &mockSystemCtl{runErr: errors.New("start failed")}
	svc := &linuxService{name: "test-svc", system: mock}

	if err := svc.Start(); err == nil {
		t.Error("expected error, got nil")
	}
}

func TestLinuxService_Stop(t *testing.T) {
	mock := &mockSystemCtl{}
	svc := &linuxService{name: "test-svc", system: mock}

	if err := svc.Stop(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(mock.runCalls) != 1 || mock.runCalls[0][0] != "stop" ||
		mock.runCalls[0][1] != "test-svc" {
		t.Errorf("expected Run(stop, test-svc), got %v", mock.runCalls)
	}
}

func TestLinuxService_Stop_Error(t *testing.T) {
	mock := &mockSystemCtl{runErr: errors.New("stop failed")}
	svc := &linuxService{name: "test-svc", system: mock}

	if err := svc.Stop(); err == nil {
		t.Error("expected error, got nil")
	}
}

func TestLinuxService_Delete(t *testing.T) {
	tmpFile := newTempConfigPath(t)
	if err := os.WriteFile(tmpFile, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	mock := &mockSystemCtl{configFilePath: tmpFile}
	svc := &linuxService{name: "test-svc", system: mock}

	if err := svc.Delete(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(mock.runCalls) != 1 || mock.runCalls[0][0] != "disable" ||
		mock.runCalls[0][1] != "test-svc" {
		t.Errorf("expected Run(disable, test-svc), got %v", mock.runCalls)
	}
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Error("expected config file to be removed")
	}
}

func TestLinuxService_Delete_DisableError(t *testing.T) {
	mock := &mockSystemCtl{runErr: errors.New("disable failed")}
	svc := &linuxService{name: "test-svc", system: mock}

	if err := svc.Delete(); err == nil {
		t.Error("expected error, got nil")
	}
}

func TestLinuxService_Delete_RemoveError(t *testing.T) {
	mock := &mockSystemCtl{configFilePath: "/nonexistent/path/file.service"}
	svc := &linuxService{name: "test-svc", system: mock}

	if err := svc.Delete(); err == nil {
		t.Error("expected error removing nonexistent file, got nil")
	}
}

func TestLinuxService_IsActive_Active(t *testing.T) {
	mock := &mockSystemCtl{}
	svc := &linuxService{name: "test-svc", system: mock}

	if !svc.IsActive() {
		t.Error("expected IsActive to return true when Run succeeds")
	}
}

func TestLinuxService_IsActive_Inactive(t *testing.T) {
	mock := &mockSystemCtl{runErr: errors.New("inactive")}
	svc := &linuxService{name: "test-svc", system: mock}

	if svc.IsActive() {
		t.Error("expected IsActive to return false when Run fails")
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
	tmpFile := newTempConfigPath(t)
	mock := &mockSystemCtl{configFilePath: tmpFile}
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
	if len(mock.runCalls) != 2 {
		t.Fatalf("expected 2 Run calls, got %d", len(mock.runCalls))
	}
	if mock.runCalls[0][0] != "daemon-reload" {
		t.Errorf("expected first Run call to be daemon-reload, got %v", mock.runCalls[0])
	}
	if mock.runCalls[1][0] != "enable" || mock.runCalls[1][1] != "test-svc" {
		t.Errorf("expected second Run call to be enable test-svc, got %v", mock.runCalls[1])
	}
}

func TestDefaultServiceManager_Create_ServiceConfigContent(t *testing.T) {
	tmpFile := newTempConfigPath(t)
	mock := &mockSystemCtl{configFilePath: tmpFile}
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
		t.Fatalf("failed to read config file: %v", err)
	}
	content := string(data)

	checks := []string{
		"[Unit]",
		"Description=my-service",
		"[Service]",
		"ExecStart=/usr/bin/agent --org-id org-abc --config-file /etc/agent.json --log-file /var/log/agent.log",
		"Restart=always",
		"[Install]",
		"WantedBy=multi-user.target",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("expected config to contain %q, got:\n%s", check, content)
		}
	}
}

func TestDefaultServiceManager_Create_WriteFileError(t *testing.T) {
	mock := &mockSystemCtl{configFilePath: "/nonexistent/dir/file.service"}
	sm := &defaultServiceManager{system: mock}

	_, err := sm.Create(AgentParams{Name: "test-svc"})

	if err == nil {
		t.Error("expected error on WriteFile failure, got nil")
	}
}

func TestDefaultServiceManager_Create_DaemonReloadError(t *testing.T) {
	mock := &mockSystemCtl{
		configFilePath: newTempConfigPath(t),
		runErrOnCmd:    "daemon-reload",
		runErr:         errors.New("daemon-reload failed"),
	}
	sm := &defaultServiceManager{system: mock}

	_, err := sm.Create(AgentParams{Name: "test-svc"})

	if err == nil {
		t.Error("expected error on daemon-reload failure, got nil")
	}
}

func TestDefaultServiceManager_Create_EnableError(t *testing.T) {
	mock := &mockSystemCtl{
		configFilePath: newTempConfigPath(t),
		runErrOnCmd:    "enable",
		runErr:         errors.New("enable failed"),
	}
	sm := &defaultServiceManager{system: mock}

	_, err := sm.Create(AgentParams{Name: "test-svc"})

	if err == nil {
		t.Error("expected error on enable failure, got nil")
	}
}

// defaultServiceManager.Open tests

func TestDefaultServiceManager_Open_Success(t *testing.T) {
	mock := &mockSystemCtl{}
	sm := &defaultServiceManager{system: mock}

	svc, err := sm.Open("test-svc")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if svc == nil {
		t.Fatal("expected service, got nil")
	}
	if len(mock.runCalls) != 1 || mock.runCalls[0][0] != "is-enabled" ||
		mock.runCalls[0][1] != "test-svc" {
		t.Errorf("expected Run(is-enabled, test-svc), got %v", mock.runCalls)
	}
}

func TestDefaultServiceManager_Open_Error(t *testing.T) {
	mock := &mockSystemCtl{runErr: errors.New("status failed")}
	sm := &defaultServiceManager{system: mock}

	_, err := sm.Open("test-svc")

	if err == nil {
		t.Error("expected error, got nil")
	}
}

// Run tests

type immediateRunner struct {
	exitCode ServiceExitCode
}

func (r *immediateRunner) Name() string { return "test" }
func (r *immediateRunner) Execute(stop <-chan struct{}, running chan<- struct{}) ServiceExitCode {
	running <- struct{}{}
	return r.exitCode
}

func TestRun_ReturnsZeroExitCode(t *testing.T) {
	code, err := Run(&immediateRunner{exitCode: 0})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
}

func TestRun_ReturnsNonZeroExitCode(t *testing.T) {
	code, err := Run(&immediateRunner{exitCode: GenericError})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if code != int(GenericError) {
		t.Errorf("expected exit code %d, got %d", GenericError, code)
	}
}
