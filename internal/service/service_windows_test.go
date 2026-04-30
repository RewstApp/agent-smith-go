//go:build windows

package service

import (
	"errors"
	"testing"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// mockWindowsServiceHandle

type mockWindowsServiceHandle struct {
	closeErr   error
	startErr   error
	deleteErr  error
	controlErr error
	queryErr   error

	controlStatus svc.Status
	queryStatuses []svc.Status
	queryCallIdx  int

	closeCalled   bool
	startCalled   bool
	deleteCalled  bool
	controlCalled bool
}

func (m *mockWindowsServiceHandle) Close() error {
	m.closeCalled = true
	return m.closeErr
}

func (m *mockWindowsServiceHandle) Start(args ...string) error {
	m.startCalled = true
	return m.startErr
}

func (m *mockWindowsServiceHandle) Delete() error {
	m.deleteCalled = true
	return m.deleteErr
}

func (m *mockWindowsServiceHandle) Control(c svc.Cmd) (svc.Status, error) {
	m.controlCalled = true
	return m.controlStatus, m.controlErr
}

func (m *mockWindowsServiceHandle) Query() (svc.Status, error) {
	if m.queryErr != nil {
		return svc.Status{}, m.queryErr
	}
	if m.queryCallIdx < len(m.queryStatuses) {
		status := m.queryStatuses[m.queryCallIdx]
		m.queryCallIdx++
		return status, nil
	}
	return svc.Status{State: svc.Stopped}, nil
}

// mockWindowsServiceManager

type mockWindowsServiceManager struct {
	createErr      error
	openErr        error
	disconnected   bool
	capturedConfig mgr.Config
}

func (m *mockWindowsServiceManager) Disconnect() error {
	m.disconnected = true
	return nil
}

func (m *mockWindowsServiceManager) CreateService(
	name, exepath string,
	c mgr.Config,
	args ...string,
) (*mgr.Service, error) {
	m.capturedConfig = c
	return nil, m.createErr
}

func (m *mockWindowsServiceManager) OpenService(name string) (*mgr.Service, error) {
	return nil, m.openErr
}

// mockWindowsServiceManagerFactory

type mockWindowsServiceManagerFactory struct {
	manager    *mockWindowsServiceManager
	connectErr error
}

func (m *mockWindowsServiceManagerFactory) Connect() (windowsServiceManager, error) {
	if m.connectErr != nil {
		return nil, m.connectErr
	}
	return m.manager, nil
}

// mockRunner

type mockRunner struct {
	name     string
	exitCode ServiceExitCode
}

func (m *mockRunner) Name() string { return m.name }
func (m *mockRunner) Execute(stop <-chan struct{}, running chan<- struct{}) ServiceExitCode {
	running <- struct{}{}
	<-stop
	return m.exitCode
}

// windowsService tests

func TestWindowsService_Close(t *testing.T) {
	handle := &mockWindowsServiceHandle{}
	s := &windowsService{handle: handle}

	if err := s.Close(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !handle.closeCalled {
		t.Error("expected Close to be called on handle")
	}
}

func TestWindowsService_Close_Error(t *testing.T) {
	handle := &mockWindowsServiceHandle{closeErr: errors.New("close failed")}
	s := &windowsService{handle: handle}

	if err := s.Close(); err == nil {
		t.Error("expected error, got nil")
	}
}

func TestWindowsService_Start(t *testing.T) {
	handle := &mockWindowsServiceHandle{}
	s := &windowsService{handle: handle}

	if err := s.Start(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !handle.startCalled {
		t.Error("expected Start to be called on handle")
	}
}

func TestWindowsService_Start_Error(t *testing.T) {
	handle := &mockWindowsServiceHandle{startErr: errors.New("start failed")}
	s := &windowsService{handle: handle}

	if err := s.Start(); err == nil {
		t.Error("expected error, got nil")
	}
}

func TestWindowsService_Delete(t *testing.T) {
	handle := &mockWindowsServiceHandle{}
	s := &windowsService{handle: handle}

	if err := s.Delete(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !handle.deleteCalled {
		t.Error("expected Delete to be called on handle")
	}
}

func TestWindowsService_Delete_Error(t *testing.T) {
	handle := &mockWindowsServiceHandle{deleteErr: errors.New("delete failed")}
	s := &windowsService{handle: handle}

	if err := s.Delete(); err == nil {
		t.Error("expected error, got nil")
	}
}

func TestWindowsService_Stop_AlreadyStopped(t *testing.T) {
	handle := &mockWindowsServiceHandle{
		controlStatus: svc.Status{State: svc.Stopped},
	}
	s := &windowsService{handle: handle}

	if err := s.Stop(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !handle.controlCalled {
		t.Error("expected Control to be called")
	}
}

func TestWindowsService_Stop_PollingUntilStopped(t *testing.T) {
	handle := &mockWindowsServiceHandle{
		controlStatus: svc.Status{State: svc.StopPending},
		queryStatuses: []svc.Status{
			{State: svc.StopPending},
			{State: svc.Stopped},
		},
	}
	s := &windowsService{handle: handle}

	if err := s.Stop(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if handle.queryCallIdx != 2 {
		t.Errorf("expected 2 Query calls, got %d", handle.queryCallIdx)
	}
}

func TestWindowsService_Stop_ControlError(t *testing.T) {
	handle := &mockWindowsServiceHandle{controlErr: errors.New("control failed")}
	s := &windowsService{handle: handle}

	if err := s.Stop(); err == nil {
		t.Error("expected error, got nil")
	}
}

func TestWindowsService_Stop_QueryError(t *testing.T) {
	handle := &mockWindowsServiceHandle{
		controlStatus: svc.Status{State: svc.StopPending},
		queryErr:      errors.New("query failed"),
	}
	s := &windowsService{handle: handle}

	if err := s.Stop(); err == nil {
		t.Error("expected error from Query, got nil")
	}
}

func TestWindowsService_IsActive_Running(t *testing.T) {
	handle := &mockWindowsServiceHandle{
		queryStatuses: []svc.Status{{State: svc.Running}},
	}
	s := &windowsService{handle: handle}

	if !s.IsActive() {
		t.Error("expected IsActive to return true when running")
	}
}

func TestWindowsService_IsActive_Stopped(t *testing.T) {
	handle := &mockWindowsServiceHandle{
		queryStatuses: []svc.Status{{State: svc.Stopped}},
	}
	s := &windowsService{handle: handle}

	if s.IsActive() {
		t.Error("expected IsActive to return false when stopped")
	}
}

func TestWindowsService_IsActive_QueryError(t *testing.T) {
	handle := &mockWindowsServiceHandle{queryErr: errors.New("query failed")}
	s := &windowsService{handle: handle}

	if s.IsActive() {
		t.Error("expected IsActive to return false on query error")
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

func TestDefaultServiceManager_Create_ConnectError(t *testing.T) {
	factory := &mockWindowsServiceManagerFactory{
		connectErr: errors.New("connect failed"),
	}
	sm := &defaultServiceManager{factory: factory}

	_, err := sm.Create(AgentParams{})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "connect failed" {
		t.Errorf("expected 'connect failed', got %q", err.Error())
	}
}

func TestDefaultServiceManager_Create_CreateServiceError(t *testing.T) {
	manager := &mockWindowsServiceManager{createErr: errors.New("create failed")}
	factory := &mockWindowsServiceManagerFactory{manager: manager}
	sm := &defaultServiceManager{factory: factory}

	_, err := sm.Create(AgentParams{})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "create failed" {
		t.Errorf("expected 'create failed', got %q", err.Error())
	}
	if !manager.disconnected {
		t.Error("expected Disconnect to be called on error")
	}
}

func TestDefaultServiceManager_Create_WithServiceUsername(t *testing.T) {
	manager := &mockWindowsServiceManager{}
	factory := &mockWindowsServiceManagerFactory{manager: manager}
	sm := &defaultServiceManager{factory: factory}

	_, err := sm.Create(AgentParams{
		ServiceUsername: `DOMAIN\svc_rewst`,
		ServicePassword: "p@ssw0rd",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if manager.capturedConfig.ServiceStartName != `DOMAIN\svc_rewst` {
		t.Errorf(
			"expected ServiceStartName 'DOMAIN\\svc_rewst', got %q",
			manager.capturedConfig.ServiceStartName,
		)
	}
	if manager.capturedConfig.Password != "p@ssw0rd" {
		t.Errorf("expected Password 'p@ssw0rd', got %q", manager.capturedConfig.Password)
	}
}

func TestDefaultServiceManager_Create_WithoutServiceUsername_NoCredentials(t *testing.T) {
	manager := &mockWindowsServiceManager{}
	factory := &mockWindowsServiceManagerFactory{manager: manager}
	sm := &defaultServiceManager{factory: factory}

	_, err := sm.Create(AgentParams{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if manager.capturedConfig.ServiceStartName != "" {
		t.Errorf(
			"expected empty ServiceStartName when not provided, got %q",
			manager.capturedConfig.ServiceStartName,
		)
	}
	if manager.capturedConfig.Password != "" {
		t.Errorf(
			"expected empty Password when not provided, got %q",
			manager.capturedConfig.Password,
		)
	}
}

func TestDefaultServiceManager_Create_GrantAccessError(t *testing.T) {
	manager := &mockWindowsServiceManager{}
	factory := &mockWindowsServiceManagerFactory{manager: manager}
	grantErr := errors.New("icacls failed")
	sm := &defaultServiceManager{
		factory: factory,
		grantAccess: func(dir, username string) error {
			return grantErr
		},
	}

	_, err := sm.Create(AgentParams{
		ConfigFilePath:  `C:\ProgramData\Rewst\config.json`,
		ServiceUsername: `.\rewst_agent_it`,
	})

	if err == nil {
		t.Error("expected error from grantAccess, got nil")
	}
}

func TestDefaultServiceManager_Create_GrantAccessError_ProgramDir(t *testing.T) {
	manager := &mockWindowsServiceManager{}
	factory := &mockWindowsServiceManagerFactory{manager: manager}
	callCount := 0
	sm := &defaultServiceManager{
		factory: factory,
		grantAccess: func(dir, username string) error {
			callCount++
			if callCount == 2 {
				return errors.New("icacls program dir failed")
			}
			return nil
		},
	}

	_, err := sm.Create(AgentParams{
		ConfigFilePath:      `C:\ProgramData\Rewst\config.json`,
		AgentExecutablePath: `C:\Program Files\Rewst\agent_smith.exe`,
		ServiceUsername:     `.\rewst_agent_it`,
	})

	if err == nil {
		t.Error("expected error from program dir grantAccess, got nil")
	}
	if callCount != 2 {
		t.Errorf("expected 2 grantAccess calls before failure, got %d", callCount)
	}
}

func TestDefaultServiceManager_Create_GrantsAllDirectories(t *testing.T) {
	manager := &mockWindowsServiceManager{}
	factory := &mockWindowsServiceManagerFactory{manager: manager}
	var grantedDirs []string
	sm := &defaultServiceManager{
		factory: factory,
		grantAccess: func(dir, username string) error {
			grantedDirs = append(grantedDirs, dir)
			return nil
		},
	}

	_, err := sm.Create(AgentParams{
		ConfigFilePath:      `C:\ProgramData\Rewst\config.json`,
		AgentExecutablePath: `C:\Program Files\Rewst\agent_smith.exe`,
		ScriptsDirectory:    `C:\RewstRemoteAgent\scripts\org1`,
		ServiceUsername:     `.\rewst_agent_it`,
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(grantedDirs) != 3 {
		t.Errorf("expected 3 grantAccess calls, got %d: %v", len(grantedDirs), grantedDirs)
	}
}

func TestDefaultServiceManager_Create_DisconnectsOnSuccess(t *testing.T) {
	manager := &mockWindowsServiceManager{}
	factory := &mockWindowsServiceManagerFactory{manager: manager}
	sm := &defaultServiceManager{factory: factory}

	_, err := sm.Create(AgentParams{})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if !manager.disconnected {
		t.Error("expected Disconnect to be called after success")
	}
}

// defaultServiceManager.Open tests

func TestDefaultServiceManager_Open_ConnectError(t *testing.T) {
	factory := &mockWindowsServiceManagerFactory{
		connectErr: errors.New("connect failed"),
	}
	sm := &defaultServiceManager{factory: factory}

	_, err := sm.Open("test-svc")

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "connect failed" {
		t.Errorf("expected 'connect failed', got %q", err.Error())
	}
}

func TestDefaultServiceManager_Open_OpenServiceError(t *testing.T) {
	manager := &mockWindowsServiceManager{openErr: errors.New("open failed")}
	factory := &mockWindowsServiceManagerFactory{manager: manager}
	sm := &defaultServiceManager{factory: factory}

	_, err := sm.Open("test-svc")

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "open failed" {
		t.Errorf("expected 'open failed', got %q", err.Error())
	}
	if !manager.disconnected {
		t.Error("expected Disconnect to be called on error")
	}
}

func TestDefaultServiceManager_Open_DisconnectsOnSuccess(t *testing.T) {
	manager := &mockWindowsServiceManager{}
	factory := &mockWindowsServiceManagerFactory{manager: manager}
	sm := &defaultServiceManager{factory: factory}

	_, err := sm.Open("test-svc")
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if !manager.disconnected {
		t.Error("expected Disconnect to be called after success")
	}
}

// mockWindowsServiceFactory

type mockWindowsServiceFactory struct {
	isWindowsServiceResult bool
	isWindowsServiceErr    error
	runErr                 error
	runCalled              bool
	runName                string
}

func (m *mockWindowsServiceFactory) IsWindowsService() (bool, error) {
	return m.isWindowsServiceResult, m.isWindowsServiceErr
}

func (m *mockWindowsServiceFactory) Run(name string, handler svc.Handler) error {
	m.runCalled = true
	m.runName = name
	return m.runErr
}

// runWithFactory tests

func TestRunWithFactory_IsWindowsServiceError(t *testing.T) {
	factory := &mockWindowsServiceFactory{
		isWindowsServiceErr: errors.New("detection failed"),
	}

	code, err := runWithFactory(&mockRunner{}, factory)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "detection failed" {
		t.Errorf("expected 'detection failed', got %q", err.Error())
	}
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
	if factory.runCalled {
		t.Error("expected Run not to be called")
	}
}

func TestRunWithFactory_NotWindowsService(t *testing.T) {
	factory := &mockWindowsServiceFactory{
		isWindowsServiceResult: false,
	}

	code, err := runWithFactory(&mockRunner{}, factory)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "executable should be run as a service" {
		t.Errorf("unexpected error: %q", err.Error())
	}
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
	if factory.runCalled {
		t.Error("expected Run not to be called")
	}
}

func TestRunWithFactory_RunError(t *testing.T) {
	factory := &mockWindowsServiceFactory{
		isWindowsServiceResult: true,
		runErr:                 errors.New("run failed"),
	}
	runner := &mockRunner{name: "test-svc"}

	code, err := runWithFactory(runner, factory)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if err.Error() != "run failed" {
		t.Errorf("expected 'run failed', got %q", err.Error())
	}
	if factory.runName != "test-svc" {
		t.Errorf("expected Run called with 'test-svc', got %q", factory.runName)
	}
	_ = code
}

func TestRunWithFactory_Success(t *testing.T) {
	factory := &mockWindowsServiceFactory{
		isWindowsServiceResult: true,
	}
	runner := &mockRunner{name: "test-svc", exitCode: 0}

	code, err := runWithFactory(runner, factory)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
	if !factory.runCalled {
		t.Error("expected Run to be called")
	}
	if factory.runName != "test-svc" {
		t.Errorf("expected Run called with 'test-svc', got %q", factory.runName)
	}
}

// windowsRunner.Execute tests

func TestWindowsRunner_Execute_SendsStartPending(t *testing.T) {
	request := make(chan svc.ChangeRequest, 1)
	response := make(chan svc.Status, 5)
	runner := &mockRunner{exitCode: 0}
	host := &windowsRunner{runner: runner}

	done := make(chan struct{})
	go func() {
		host.Execute(nil, request, response)
		close(done)
	}()

	first := <-response
	if first.State != svc.StartPending {
		t.Errorf("expected StartPending, got %v", first.State)
	}

	// send stop to unblock Execute
	request <- svc.ChangeRequest{Cmd: svc.Stop}
	<-done
}

func TestWindowsRunner_Execute_SendsRunningThenStopped(t *testing.T) {
	request := make(chan svc.ChangeRequest, 1)
	response := make(chan svc.Status, 5)
	runner := &mockRunner{exitCode: 0}
	host := &windowsRunner{runner: runner}

	done := make(chan struct{})
	go func() {
		host.Execute(nil, request, response)
		close(done)
	}()

	states := []svc.State{}
	// collect StartPending + Running
	states = append(states, (<-response).State)
	states = append(states, (<-response).State)

	if states[0] != svc.StartPending {
		t.Errorf("expected StartPending first, got %v", states[0])
	}
	if states[1] != svc.Running {
		t.Errorf("expected Running second, got %v", states[1])
	}

	request <- svc.ChangeRequest{Cmd: svc.Stop}

	stopped := <-response
	if stopped.State != svc.Stopped {
		t.Errorf("expected Stopped, got %v", stopped.State)
	}
	<-done
}

func TestWindowsRunner_Execute_ReturnsExitCode(t *testing.T) {
	request := make(chan svc.ChangeRequest, 1)
	response := make(chan svc.Status, 5)
	runner := &mockRunner{exitCode: GenericError}
	host := &windowsRunner{runner: runner}

	done := make(chan struct{})
	var ok bool
	var code uint32
	go func() {
		ok, code = host.Execute(nil, request, response)
		close(done)
	}()

	// drain StartPending + Running
	<-response
	<-response

	request <- svc.ChangeRequest{Cmd: svc.Stop}
	<-done

	if ok {
		t.Error("expected ok=false for non-zero exit code")
	}
	if code != uint32(GenericError) {
		t.Errorf("expected exit code %d, got %d", GenericError, code)
	}
}

func TestWindowsRunner_Execute_ShutdownAlsoStops(t *testing.T) {
	request := make(chan svc.ChangeRequest, 1)
	response := make(chan svc.Status, 5)
	runner := &mockRunner{exitCode: 0}
	host := &windowsRunner{runner: runner}

	done := make(chan struct{})
	go func() {
		host.Execute(nil, request, response)
		close(done)
	}()

	<-response // StartPending
	<-response // Running

	request <- svc.ChangeRequest{Cmd: svc.Shutdown}

	stopped := <-response
	if stopped.State != svc.Stopped {
		t.Errorf("expected Stopped on Shutdown, got %v", stopped.State)
	}
	<-done
}
