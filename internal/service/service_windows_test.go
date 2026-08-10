//go:build windows

package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// fakeClock advances only when the code under test sleeps, so the bounded stop
// wait can be exercised deterministically and instantly.
type fakeClock struct {
	now   time.Time
	slept []time.Duration
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Sleep(d time.Duration) {
	c.now = c.now.Add(d)
	c.slept = append(c.slept, d)
}

// mockWindowsServiceHandle

type mockWindowsServiceHandle struct {
	closeErr   error
	startErr   error
	deleteErr  error
	controlErr error
	queryErr   error

	controlStatus svc.Status
	queryStatuses []svc.Status
	// queryFallback, when set, is returned once queryStatuses is exhausted.
	// Without it the mock falls back to Stopped, which no bounded-wait test can
	// use because the service would always eventually stop.
	queryFallback *svc.Status
	// queryHook, when set, is consulted before each Query result and can fail a
	// specific call by its zero-based index.
	queryHook    func(callIdx int) error
	queryCallIdx int

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
	if m.queryHook != nil {
		if err := m.queryHook(m.queryCallIdx); err != nil {
			m.queryCallIdx++
			return svc.Status{}, err
		}
	}
	if m.queryCallIdx < len(m.queryStatuses) {
		status := m.queryStatuses[m.queryCallIdx]
		m.queryCallIdx++
		return status, nil
	}
	m.queryCallIdx++
	if m.queryFallback != nil {
		return *m.queryFallback, nil
	}
	return svc.Status{State: svc.Stopped}, nil
}

// mockWindowsServiceManager

type mockWindowsServiceManager struct {
	createErr    error
	openErr      error
	disconnected bool

	lastCreateConfig mgr.Config
	createCalled     bool
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
	m.createCalled = true
	m.lastCreateConfig = c
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
	clock := &fakeClock{}
	s := &windowsService{handle: handle, now: clock.Now, sleep: clock.Sleep}

	if err := s.Stop(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if handle.queryCallIdx != 2 {
		t.Errorf("expected 2 Query calls, got %d", handle.queryCallIdx)
	}
	// Polls on the documented interval.
	if len(clock.slept) != 2 || clock.slept[0] != pollingInterval {
		t.Errorf("expected polls at %s, got %v", pollingInterval, clock.slept)
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
	clock := &fakeClock{}
	s := &windowsService{handle: handle, now: clock.Now, sleep: clock.Sleep}

	if err := s.Stop(); err == nil {
		t.Error("expected error from Query, got nil")
	}
}

func TestWindowsService_Stop_TimesOutWhileStopPending(t *testing.T) {
	stopPending := svc.Status{State: svc.StopPending}
	handle := &mockWindowsServiceHandle{
		controlStatus: stopPending,
		queryFallback: &stopPending,
	}
	clock := &fakeClock{}
	s := &windowsService{
		handle:       handle,
		name:         "rewst_agent_smith_test-org",
		stopTimeout:  10 * time.Millisecond,
		pollInterval: time.Millisecond,
		now:          clock.Now,
		sleep:        clock.Sleep,
	}

	err := s.Stop()
	if err == nil {
		t.Fatal("expected an error when the service never leaves StopPending")
	}
	for _, want := range []string{"rewst_agent_smith_test-org", "10ms", "StopPending"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("expected error to mention %q, got %q", want, err.Error())
		}
	}
	// Bounded: polls for the deadline and no longer.
	if len(clock.slept) != 10 {
		t.Errorf("expected 10 polls within the deadline, got %d", len(clock.slept))
	}
	if handle.queryCallIdx != 10 {
		t.Errorf("expected 10 Query calls, got %d", handle.queryCallIdx)
	}
}

func TestWindowsService_Stop_StoppedOnLastPollBeforeDeadline(t *testing.T) {
	handle := &mockWindowsServiceHandle{
		controlStatus: svc.Status{State: svc.StopPending},
		queryStatuses: []svc.Status{
			{State: svc.StopPending},
			{State: svc.StopPending},
			{State: svc.StopPending},
			{State: svc.Stopped},
		},
	}
	clock := &fakeClock{}
	s := &windowsService{
		handle:       handle,
		name:         "rewst_agent_smith_test-org",
		stopTimeout:  4 * time.Millisecond,
		pollInterval: time.Millisecond,
		now:          clock.Now,
		sleep:        clock.Sleep,
	}

	if err := s.Stop(); err != nil {
		t.Fatalf("expected no error when the service stops on the last poll, got %v", err)
	}
	if handle.queryCallIdx != 4 {
		t.Errorf("expected 4 Query calls, got %d", handle.queryCallIdx)
	}
}

func TestWindowsService_Stop_UnexpectedStateFailsFast(t *testing.T) {
	handle := &mockWindowsServiceHandle{
		controlStatus: svc.Status{State: svc.Paused},
	}
	clock := &fakeClock{}
	s := &windowsService{
		handle:       handle,
		name:         "rewst_agent_smith_test-org",
		stopTimeout:  time.Hour,
		pollInterval: time.Millisecond,
		now:          clock.Now,
		sleep:        clock.Sleep,
	}

	err := s.Stop()
	if err == nil {
		t.Fatal("expected an error for a state the service cannot stop from")
	}
	if !strings.Contains(err.Error(), "Paused") {
		t.Errorf("expected error to name the observed state, got %q", err.Error())
	}
	// Fails immediately rather than burning the deadline.
	if len(clock.slept) != 0 {
		t.Errorf("expected no polling, got %d sleeps", len(clock.slept))
	}
	if handle.queryCallIdx != 0 {
		t.Errorf("expected no Query calls, got %d", handle.queryCallIdx)
	}
}

func TestWindowsService_Stop_QueryErrorMidPoll(t *testing.T) {
	stopPending := svc.Status{State: svc.StopPending}
	handle := &mockWindowsServiceHandle{
		controlStatus: stopPending,
		queryFallback: &stopPending,
	}
	clock := &fakeClock{}
	s := &windowsService{
		handle:       handle,
		name:         "rewst_agent_smith_test-org",
		stopTimeout:  time.Hour,
		pollInterval: time.Millisecond,
		now:          clock.Now,
		sleep:        clock.Sleep,
	}

	// Fail the third Query, after the wait is already under way.
	handle.queryStatuses = []svc.Status{stopPending, stopPending}
	handle.queryHook = func(callIdx int) error {
		if callIdx >= 2 {
			return errors.New("query failed")
		}
		return nil
	}

	err := s.Stop()
	if err == nil {
		t.Fatal("expected the Query error to be returned")
	}
	if err.Error() != "query failed" {
		t.Errorf("expected 'query failed', got %q", err.Error())
	}
	if len(clock.slept) != 3 {
		t.Errorf("expected 3 polls before the failure, got %d", len(clock.slept))
	}
}

func TestWindowsService_Stop_RunningIsPolledNotRejected(t *testing.T) {
	handle := &mockWindowsServiceHandle{
		// Control can report the pre-stop state before the service thread picks
		// the control up; that must not be treated as a refusal to stop.
		controlStatus: svc.Status{State: svc.Running},
		queryStatuses: []svc.Status{
			{State: svc.StopPending},
			{State: svc.Stopped},
		},
	}
	clock := &fakeClock{}
	s := &windowsService{
		handle:       handle,
		name:         "rewst_agent_smith_test-org",
		stopTimeout:  time.Minute,
		pollInterval: time.Millisecond,
		now:          clock.Now,
		sleep:        clock.Sleep,
	}

	if err := s.Stop(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if handle.queryCallIdx != 2 {
		t.Errorf("expected 2 Query calls, got %d", handle.queryCallIdx)
	}
}

func TestWindowsService_ResolveStopTimeout(t *testing.T) {
	orig := stopTimeoutOverrideStr
	t.Cleanup(func() { stopTimeoutOverrideStr = orig })

	// No override: the documented constant.
	stopTimeoutOverrideStr = ""
	s := &windowsService{}
	if got := s.resolveStopTimeout(); got != serviceStopTimeout {
		t.Errorf("expected %s, got %s", serviceStopTimeout, got)
	}

	// A valid, positive override wins (integration builds).
	stopTimeoutOverrideStr = "25s"
	if got := s.resolveStopTimeout(); got != 25*time.Second {
		t.Errorf("expected 25s, got %s", got)
	}

	// Garbage and non-positive overrides fall back rather than disabling the bound.
	for _, bad := range []string{"nonsense", "0s", "-5s"} {
		stopTimeoutOverrideStr = bad
		if got := s.resolveStopTimeout(); got != serviceStopTimeout {
			t.Errorf("override %q: expected fallback to %s, got %s", bad, serviceStopTimeout, got)
		}
	}

	// The per-instance value (unit tests) wins over the override.
	stopTimeoutOverrideStr = "25s"
	s.stopTimeout = time.Millisecond
	if got := s.resolveStopTimeout(); got != time.Millisecond {
		t.Errorf("expected 1ms, got %s", got)
	}
}

func TestServiceStateName(t *testing.T) {
	cases := map[svc.State]string{
		svc.Stopped:         "Stopped",
		svc.StartPending:    "StartPending",
		svc.StopPending:     "StopPending",
		svc.Running:         "Running",
		svc.ContinuePending: "ContinuePending",
		svc.PausePending:    "PausePending",
		svc.Paused:          "Paused",
		svc.State(99):       "Unknown(99)",
	}

	for state, want := range cases {
		if got := serviceStateName(state); got != want {
			t.Errorf("serviceStateName(%d) = %q, want %q", uint32(state), got, want)
		}
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

func TestDefaultServiceManager_Create_DefaultsToLocalSystem(t *testing.T) {
	manager := &mockWindowsServiceManager{}
	factory := &mockWindowsServiceManagerFactory{manager: manager}
	sm := &defaultServiceManager{factory: factory}

	_, err := sm.Create(AgentParams{Name: "test-svc"})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if !manager.createCalled {
		t.Fatal("expected CreateService to be called")
	}
	if manager.lastCreateConfig.ServiceStartName != "" {
		t.Errorf(
			"expected empty ServiceStartName when ServiceUsername unset, got %q",
			manager.lastCreateConfig.ServiceStartName,
		)
	}
	if manager.lastCreateConfig.Password != "" {
		t.Errorf(
			"expected empty Password when ServiceUsername unset, got %q",
			manager.lastCreateConfig.Password,
		)
	}
}

func TestDefaultServiceManager_Create_AppliesServiceCredentials(t *testing.T) {
	manager := &mockWindowsServiceManager{}
	factory := &mockWindowsServiceManagerFactory{manager: manager}
	sm := &defaultServiceManager{factory: factory}

	_, err := sm.Create(AgentParams{
		Name:            "test-svc",
		ServiceUsername: "DOMAIN\\svc_rewst",
		ServicePassword: "p@ss",
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if manager.lastCreateConfig.ServiceStartName != "DOMAIN\\svc_rewst" {
		t.Errorf(
			"expected ServiceStartName %q, got %q",
			"DOMAIN\\svc_rewst",
			manager.lastCreateConfig.ServiceStartName,
		)
	}
	if manager.lastCreateConfig.Password != "p@ss" {
		t.Errorf("expected Password %q, got %q", "p@ss", manager.lastCreateConfig.Password)
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
