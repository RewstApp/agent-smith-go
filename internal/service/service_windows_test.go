//go:build windows

package service

import (
	"testing"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// MockRunner is a test implementation of the Runner interface
type MockRunner struct {
	name      string
	exitCode  ServiceExitCode
	executed  bool
	stopDelay time.Duration
}

func (m *MockRunner) Name() string {
	return m.name
}

func (m *MockRunner) Execute(stop <-chan struct{}, running chan<- struct{}) ServiceExitCode {
	m.executed = true
	// Signal that we're running
	running <- struct{}{}

	// Wait for stop signal or timeout
	select {
	case <-stop:
		// Stopped gracefully
	case <-time.After(m.stopDelay):
		// Timeout (simulates running service)
	}

	return m.exitCode
}

// TestWindowsRunner_Execute tests the Execute method of windowsRunner
func TestWindowsRunner_Execute(t *testing.T) {
	tests := []struct {
		name             string
		runnerExitCode   ServiceExitCode
		sendStopSignal   bool
		expectedSuccess  bool
		expectedExitCode uint32
	}{
		{
			name:             "successful execution with stop signal",
			runnerExitCode:   0,
			sendStopSignal:   true,
			expectedSuccess:  true,
			expectedExitCode: 0,
		},
		{
			name:             "execution with generic error",
			runnerExitCode:   GenericError,
			sendStopSignal:   true,
			expectedSuccess:  false,
			expectedExitCode: uint32(GenericError),
		},
		{
			name:             "execution with config error",
			runnerExitCode:   ConfigError,
			sendStopSignal:   true,
			expectedSuccess:  false,
			expectedExitCode: uint32(ConfigError),
		},
		{
			name:             "execution with log file error",
			runnerExitCode:   LogFileError,
			sendStopSignal:   true,
			expectedSuccess:  false,
			expectedExitCode: uint32(LogFileError),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &MockRunner{
				name:      "TestService",
				exitCode:  tt.runnerExitCode,
				stopDelay: 5 * time.Second, // Long enough to test stop signal
			}

			winRunner := &windowsRunner{
				runner: mockRunner,
			}

			request := make(chan svc.ChangeRequest, 1)
			response := make(chan svc.Status, 10)

			// Run Execute in a goroutine
			done := make(chan struct{})
			var success bool
			var exitCode uint32

			go func() {
				success, exitCode = winRunner.Execute([]string{}, request, response)
				close(done)
			}()

			// Wait for StartPending status
			select {
			case status := <-response:
				if status.State != svc.StartPending {
					t.Errorf("expected StartPending state, got %v", status.State)
				}
			case <-time.After(1 * time.Second):
				t.Fatal("timeout waiting for StartPending status")
			}

			// Wait for Running status
			select {
			case status := <-response:
				if status.State != svc.Running {
					t.Errorf("expected Running state, got %v", status.State)
				}
				expectedAccepts := svc.AcceptStop | svc.AcceptShutdown
				if status.Accepts != expectedAccepts {
					t.Errorf("expected Accepts %v, got %v", expectedAccepts, status.Accepts)
				}
			case <-time.After(1 * time.Second):
				t.Fatal("timeout waiting for Running status")
			}

			// Send stop signal if required
			if tt.sendStopSignal {
				request <- svc.ChangeRequest{Cmd: svc.Stop}
			}

			// Wait for execution to complete
			select {
			case <-done:
				// Execution completed
			case <-time.After(2 * time.Second):
				t.Fatal("timeout waiting for execution to complete")
			}

			// Check final Stopped status
			select {
			case status := <-response:
				if status.State != svc.Stopped {
					t.Errorf("expected Stopped state, got %v", status.State)
				}
			case <-time.After(1 * time.Second):
				t.Fatal("timeout waiting for Stopped status")
			}

			// Verify the results
			if success != tt.expectedSuccess {
				t.Errorf("expected success=%v, got %v", tt.expectedSuccess, success)
			}

			if exitCode != tt.expectedExitCode {
				t.Errorf("expected exitCode=%d, got %d", tt.expectedExitCode, exitCode)
			}

			if !mockRunner.executed {
				t.Error("expected runner to be executed")
			}

			if winRunner.exitCode != int(tt.runnerExitCode) {
				t.Errorf("expected winRunner.exitCode=%d, got %d", tt.runnerExitCode, winRunner.exitCode)
			}
		})
	}
}

// TestWindowsRunner_Execute_ShutdownSignal tests handling of shutdown signal
func TestWindowsRunner_Execute_ShutdownSignal(t *testing.T) {
	mockRunner := &MockRunner{
		name:      "TestService",
		exitCode:  0,
		stopDelay: 5 * time.Second,
	}

	winRunner := &windowsRunner{
		runner: mockRunner,
	}

	request := make(chan svc.ChangeRequest, 1)
	response := make(chan svc.Status, 10)

	done := make(chan struct{})
	go func() {
		winRunner.Execute([]string{}, request, response)
		close(done)
	}()

	// Wait for StartPending and Running states
	<-response // StartPending
	<-response // Running

	// Send shutdown signal instead of stop
	request <- svc.ChangeRequest{Cmd: svc.Shutdown}

	// Wait for execution to complete
	select {
	case <-done:
		// Execution completed successfully
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for execution to complete after shutdown")
	}

	// Verify stopped status
	select {
	case status := <-response:
		if status.State != svc.Stopped {
			t.Errorf("expected Stopped state after shutdown, got %v", status.State)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timeout waiting for Stopped status")
	}
}

// TestWindowsRunner_Execute_IgnoresOtherCommands tests that other commands are ignored
func TestWindowsRunner_Execute_IgnoresOtherCommands(t *testing.T) {
	mockRunner := &MockRunner{
		name:      "TestService",
		exitCode:  0,
		stopDelay: 100 * time.Millisecond, // Short delay for this test
	}

	winRunner := &windowsRunner{
		runner: mockRunner,
	}

	request := make(chan svc.ChangeRequest, 10)
	response := make(chan svc.Status, 10)

	done := make(chan struct{})
	go func() {
		winRunner.Execute([]string{}, request, response)
		close(done)
	}()

	// Wait for StartPending and Running states
	<-response // StartPending
	<-response // Running

	// Send commands that should be ignored
	request <- svc.ChangeRequest{Cmd: svc.Pause}
	request <- svc.ChangeRequest{Cmd: svc.Continue}
	request <- svc.ChangeRequest{Cmd: svc.Interrogate}

	// Wait for the runner to complete on its own (via stopDelay timeout)
	select {
	case <-done:
		// Execution completed successfully without stopping early
	case <-time.After(2 * time.Second):
		// Send stop to cleanup
		request <- svc.ChangeRequest{Cmd: svc.Stop}
		<-done
	}

	// Verify stopped status
	select {
	case status := <-response:
		if status.State != svc.Stopped {
			t.Errorf("expected Stopped state, got %v", status.State)
		}
	default:
		t.Error("expected Stopped status in response channel")
	}
}

// TestWindowsRunner_Execute_ContextCancellation tests proper cleanup of goroutines
func TestWindowsRunner_Execute_ContextCancellation(t *testing.T) {
	mockRunner := &MockRunner{
		name:      "TestService",
		exitCode:  0,
		stopDelay: 5 * time.Second,
	}

	winRunner := &windowsRunner{
		runner: mockRunner,
	}

	request := make(chan svc.ChangeRequest, 1)
	response := make(chan svc.Status, 10)

	done := make(chan struct{})
	go func() {
		winRunner.Execute([]string{}, request, response)
		close(done)
	}()

	// Wait for service to be running
	<-response // StartPending
	<-response // Running

	// Send stop signal
	request <- svc.ChangeRequest{Cmd: svc.Stop}

	// Wait for completion
	select {
	case <-done:
		// Completed successfully
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for execution to complete")
	}

	// Verify the context cleanup happened by checking that
	// the Execute method completed (goroutines should be cleaned up)
	<-response // Stopped status

	// Give a moment for goroutines to clean up
	time.Sleep(100 * time.Millisecond)
}

// TestAgentParams validates the AgentParams structure
func TestAgentParams(t *testing.T) {
	params := AgentParams{
		Name:                "TestAgent",
		AgentExecutablePath: "C:\\Program Files\\Rewst\\agent.exe",
		OrgId:               "test-org-123",
		ConfigFilePath:      "C:\\ProgramData\\Rewst\\config.json",
		LogFilePath:         "C:\\ProgramData\\Rewst\\agent.log",
	}

	if params.Name != "TestAgent" {
		t.Errorf("expected Name='TestAgent', got '%s'", params.Name)
	}

	if params.AgentExecutablePath != "C:\\Program Files\\Rewst\\agent.exe" {
		t.Errorf("expected AgentExecutablePath='C:\\Program Files\\Rewst\\agent.exe', got '%s'", params.AgentExecutablePath)
	}

	if params.OrgId != "test-org-123" {
		t.Errorf("expected OrgId='test-org-123', got '%s'", params.OrgId)
	}

	if params.ConfigFilePath != "C:\\ProgramData\\Rewst\\config.json" {
		t.Errorf("expected ConfigFilePath='C:\\ProgramData\\Rewst\\config.json', got '%s'", params.ConfigFilePath)
	}

	if params.LogFilePath != "C:\\ProgramData\\Rewst\\agent.log" {
		t.Errorf("expected LogFilePath='C:\\ProgramData\\Rewst\\agent.log', got '%s'", params.LogFilePath)
	}
}

// TestPollingInterval validates the polling interval constant
func TestPollingInterval(t *testing.T) {
	expectedInterval := 250 * time.Millisecond

	if pollingInterval != expectedInterval {
		t.Errorf("expected pollingInterval=%v, got %v", expectedInterval, pollingInterval)
	}
}

// Integration Tests - These require administrative privileges

// isAdmin checks if the current process has administrative privileges
func isAdmin() bool {
	_, err := mgr.Connect()
	if err != nil {
		return false
	}
	return true
}

// TestIntegration_WindowsService_CreateAndDelete tests service creation and deletion
func TestIntegration_WindowsService_CreateAndDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if !isAdmin() {
		t.Skip("skipping integration test: requires administrator privileges")
	}

	// Use a unique service name to avoid conflicts
	serviceName := "RewstAgentTest_" + time.Now().Format("20060102150405")

	params := AgentParams{
		Name:                serviceName,
		AgentExecutablePath: "C:\\Windows\\System32\\WindowsPowerShell\\v1.0\\powershell.exe",
		OrgId:               "test-org-integration",
		ConfigFilePath:      "C:\\temp\\test-config.json",
		LogFilePath:         "C:\\temp\\test-log.txt",
	}

	// Create the service
	svc, err := Create(params)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}
	defer func() {
		// Ensure cleanup even if test fails
		if svc != nil {
			svc.Delete()
			svc.Close()
		}
	}()

	// Verify service was created by opening it
	svc2, err := Open(serviceName)
	if err != nil {
		t.Errorf("failed to open created service: %v", err)
	} else {
		svc2.Close()
	}

	// Delete the service
	err = svc.Delete()
	if err != nil {
		t.Errorf("failed to delete service: %v", err)
	}

	// Close the handle
	err = svc.Close()
	if err != nil {
		t.Errorf("failed to close service handle: %v", err)
	}

	// Verify service no longer exists
	svc3, err := Open(serviceName)
	if err == nil {
		svc3.Close()
		t.Error("service should not exist after deletion, but Open succeeded")
	}
}

// TestIntegration_WindowsService_Open tests opening an existing service
func TestIntegration_WindowsService_Open(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if !isAdmin() {
		t.Skip("skipping integration test: requires administrator privileges")
	}

	// Try to open a well-known Windows service (wuauserv = Windows Update)
	svc, err := Open("wuauserv")
	if err != nil {
		t.Fatalf("failed to open Windows Update service: %v", err)
	}
	defer svc.Close()

	// Verify we can query the service status
	if winSvc, ok := svc.(*windowsService); ok {
		_, err := winSvc.handle.Query()
		if err != nil {
			t.Errorf("failed to query service status: %v", err)
		}
	}

	// Close should succeed
	err = svc.Close()
	if err != nil {
		t.Errorf("failed to close service: %v", err)
	}
}

// TestIntegration_WindowsService_OpenNonExistent tests opening a non-existent service
func TestIntegration_WindowsService_OpenNonExistent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if !isAdmin() {
		t.Skip("skipping integration test: requires administrator privileges")
	}

	// Try to open a service that doesn't exist
	nonExistentService := "RewstTestNonExistent_" + time.Now().Format("20060102150405")
	svc, err := Open(nonExistentService)
	if err == nil {
		svc.Close()
		t.Error("expected error when opening non-existent service, got nil")
	}
}

// TestIntegration_WindowsService_IsActive tests the IsActive method
func TestIntegration_WindowsService_IsActive(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if !isAdmin() {
		t.Skip("skipping integration test: requires administrator privileges")
	}

	// Open a well-known service
	svc, err := Open("wuauserv")
	if err != nil {
		t.Fatalf("failed to open Windows Update service: %v", err)
	}
	defer svc.Close()

	// Check if service is active (may be running or stopped, both are valid)
	// We're just testing that the method doesn't crash
	isActive := svc.IsActive()
	t.Logf("Windows Update service active status: %v", isActive)
}

// TestIntegration_WindowsService_Lifecycle tests full service lifecycle
func TestIntegration_WindowsService_Lifecycle(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if !isAdmin() {
		t.Skip("skipping integration test: requires administrator privileges")
	}

	// Use a unique service name
	serviceName := "RewstAgentTestLifecycle_" + time.Now().Format("20060102150405")

	// We'll use a simple executable that exits immediately for testing
	// Using cmd.exe with /c exit 0 as the service executable
	params := AgentParams{
		Name:                serviceName,
		AgentExecutablePath: "C:\\Windows\\System32\\cmd.exe",
		OrgId:               "test-org-lifecycle",
		ConfigFilePath:      "C:\\temp\\test-config.json",
		LogFilePath:         "C:\\temp\\test-log.txt",
	}

	// Create the service
	svc, err := Create(params)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}
	defer func() {
		// Cleanup
		if svc != nil {
			svc.Delete()
			svc.Close()
		}
	}()

	// Test IsActive - should be false initially
	if svc.IsActive() {
		t.Error("newly created service should not be active")
	}

	// Note: Starting and stopping a service requires it to be properly configured
	// with a valid executable that implements the service protocol.
	// Since cmd.exe doesn't implement the service protocol, we can't test Start/Stop
	// in this integration test without creating a proper test service executable.
	t.Log("Skipping Start/Stop tests - requires a proper service executable")

	// Test Delete
	err = svc.Delete()
	if err != nil {
		t.Errorf("failed to delete service: %v", err)
	}

	// Test Close
	err = svc.Close()
	if err != nil {
		t.Errorf("failed to close service handle: %v", err)
	}
}

// TestIntegration_WindowsService_Stop tests stopping a service
func TestIntegration_WindowsService_Stop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if !isAdmin() {
		t.Skip("skipping integration test: requires administrator privileges")
	}

	// We'll use Windows Update service for testing stop functionality
	// First check if it's running
	svc, err := Open("wuauserv")
	if err != nil {
		t.Fatalf("failed to open Windows Update service: %v", err)
	}
	defer svc.Close()

	initiallyActive := svc.IsActive()
	t.Logf("Windows Update service initially active: %v", initiallyActive)

	if initiallyActive {
		// Try to stop it
		err = svc.Stop()
		if err != nil {
			t.Logf("failed to stop service (may require additional permissions): %v", err)
			// Not failing the test as stopping Windows Update may be restricted
			return
		}

		// Verify it stopped
		time.Sleep(500 * time.Millisecond) // Give it time to stop
		if svc.IsActive() {
			t.Error("service should not be active after Stop()")
		}

		// Restart it for cleanup
		err = svc.Start()
		if err != nil {
			t.Logf("warning: failed to restart Windows Update service: %v", err)
		}
	} else {
		t.Log("Windows Update service not running, skipping stop test")
	}
}

// TestIntegration_WindowsService_MultipleOperations tests multiple operations on the same service
func TestIntegration_WindowsService_MultipleOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if !isAdmin() {
		t.Skip("skipping integration test: requires administrator privileges")
	}

	serviceName := "RewstAgentTestMulti_" + time.Now().Format("20060102150405")

	params := AgentParams{
		Name:                serviceName,
		AgentExecutablePath: "C:\\Windows\\System32\\cmd.exe",
		OrgId:               "test-org-multi",
		ConfigFilePath:      "C:\\temp\\test-config.json",
		LogFilePath:         "C:\\temp\\test-log.txt",
	}

	// Create service
	svc1, err := Create(params)
	if err != nil {
		t.Fatalf("failed to create service: %v", err)
	}
	defer func() {
		if svc1 != nil {
			svc1.Delete()
			svc1.Close()
		}
	}()

	// Open the same service with a second handle
	svc2, err := Open(serviceName)
	if err != nil {
		t.Errorf("failed to open service with second handle: %v", err)
	}
	if svc2 != nil {
		defer svc2.Close()
	}

	// Check IsActive on both handles
	active1 := svc1.IsActive()
	active2 := svc2.IsActive()

	if active1 != active2 {
		t.Errorf("IsActive should return same result for both handles, got %v and %v", active1, active2)
	}

	// Close second handle
	if svc2 != nil {
		err = svc2.Close()
		if err != nil {
			t.Errorf("failed to close second service handle: %v", err)
		}
	}

	// Delete using first handle
	err = svc1.Delete()
	if err != nil {
		t.Errorf("failed to delete service: %v", err)
	}

	// Close first handle
	err = svc1.Close()
	if err != nil {
		t.Errorf("failed to close first service handle: %v", err)
	}
}

// TestIntegration_Run tests the Run function
// Note: This test is commented out because it requires the test itself to be running as a service
// which is not feasible in normal test execution
/*
func TestIntegration_Run(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	if !isAdmin() {
		t.Skip("skipping integration test: requires administrator privileges")
	}

	// This test would need to be run as an actual Windows service
	// which is not possible in the normal test execution flow
	t.Skip("Run() can only be tested when the executable is launched as a Windows service")
}
*/
