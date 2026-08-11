package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
)

// ── helpers ───────────────────────────────────────────────────────────────────

func validDeviceJSON(orgId string) []byte {
	b, _ := json.Marshal(agent.Device{
		DeviceId:        "device-123",
		RewstOrgId:      orgId,
		RewstEngineHost: "engine.example.com",
		SharedAccessKey: "key123",
		AzureIotHubHost: "hub.example.com",
	})
	return b
}

// newUpdateTestFS returns a FS mock where ReadFile returns valid device JSON on
// the first call (config file) and binary content on the second (executable).
func newUpdateTestFS() *mockFileSystem {
	readCall := 0
	return &mockFileSystem{
		executableFunc: func() (string, error) { return "/fake/agent", nil },
		readFileFunc: func(string) ([]byte, error) {
			readCall++
			if readCall == 1 {
				return validDeviceJSON("test-org"), nil
			}
			return []byte("binary"), nil
		},
		writeFileFunc: func(string, []byte, os.FileMode) error { return nil },
		mkdirAllFunc:  func(string) error { return nil },
		removeAllFunc: func(string) error { return nil },
	}
}

func newBaseUpdateParams() *updateContext {
	return &updateContext{
		OrgId:          "test-org",
		LoggingLevel:   string(utils.Default),
		Sys:            newConfigTestSys(),
		Domain:         &mockDomainInfoProvider{},
		FS:             newUpdateTestFS(),
		ServiceManager: &mockServiceManager{openService: &mockService{isActive: false}},
		exitWait:       stubExitWait(),
	}
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestRunUpdate_Success(t *testing.T) {
	runUpdate(newBaseUpdateParams())
}

// deviceWithTuningJSON returns valid device JSON that already carries tuning
// overrides, simulating a config file written by a previous invocation.
func deviceWithTuningJSON(
	orgId string,
	timeout, workers, queue, attempts, backoff, cmdTimeout int,
) []byte {
	b, _ := json.Marshal(agent.Device{
		DeviceId:                        "device-123",
		RewstOrgId:                      orgId,
		RewstEngineHost:                 "engine.example.com",
		SharedAccessKey:                 "key123",
		AzureIotHubHost:                 "hub.example.com",
		MqttConnectTimeoutSeconds:       &timeout,
		WorkerCount:                     &workers,
		MessageQueueSize:                &queue,
		PostbackMaxAttempts:             &attempts,
		PostbackBaseRetryBackoffSeconds: &backoff,
		CommandTimeoutSeconds:           &cmdTimeout,
	})
	return b
}

// captureUpdateFS returns an FS that serves the given config JSON on the first
// read (binary afterwards) and records the config bytes written back.
func captureUpdateFS(configJSON []byte, written *agent.Device) *mockFileSystem {
	readCall := 0
	return &mockFileSystem{
		executableFunc: func() (string, error) { return "/fake/agent", nil },
		readFileFunc: func(string) ([]byte, error) {
			readCall++
			if readCall == 1 {
				return configJSON, nil
			}
			return []byte("binary"), nil
		},
		writeFileFunc: func(_ string, data []byte, _ os.FileMode) error {
			var device agent.Device
			if err := json.Unmarshal(data, &device); err == nil && device.DeviceId != "" {
				*written = device
			}
			return nil
		},
		mkdirAllFunc:  func(string) error { return nil },
		removeAllFunc: func(string) error { return nil },
	}
}

func TestRunUpdate_AppliesProvidedTuningFlags(t *testing.T) {
	var written agent.Device
	params := newBaseUpdateParams()
	params.FS = captureUpdateFS(deviceWithTuningJSON("test-org", 10, 1, 10, 1, 1, 1), &written)
	params.Tuning = tuningFlags{
		MqttConnectTimeoutSeconds:       45,
		MqttSubscribeTimeoutSeconds:     60,
		WorkerCount:                     20,
		MessageQueueSize:                250,
		PostbackMaxAttempts:             5,
		PostbackBaseRetryBackoffSeconds: 2,
		CommandTimeoutSeconds:           120,
	}

	runUpdate(params)

	if written.MqttConnectTimeoutSeconds == nil || *written.MqttConnectTimeoutSeconds != 45 {
		t.Errorf("expected MqttConnectTimeoutSeconds 45, got %v", written.MqttConnectTimeoutSeconds)
	}
	if written.MqttSubscribeTimeoutSeconds == nil || *written.MqttSubscribeTimeoutSeconds != 60 {
		t.Errorf(
			"expected MqttSubscribeTimeoutSeconds 60, got %v",
			written.MqttSubscribeTimeoutSeconds,
		)
	}
	if written.WorkerCount == nil || *written.WorkerCount != 20 {
		t.Errorf("expected WorkerCount 20, got %v", written.WorkerCount)
	}
	if written.MessageQueueSize == nil || *written.MessageQueueSize != 250 {
		t.Errorf("expected MessageQueueSize 250, got %v", written.MessageQueueSize)
	}
	if written.PostbackMaxAttempts == nil || *written.PostbackMaxAttempts != 5 {
		t.Errorf("expected PostbackMaxAttempts 5, got %v", written.PostbackMaxAttempts)
	}
	if written.PostbackBaseRetryBackoffSeconds == nil ||
		*written.PostbackBaseRetryBackoffSeconds != 2 {
		t.Errorf(
			"expected PostbackBaseRetryBackoffSeconds 2, got %v",
			written.PostbackBaseRetryBackoffSeconds,
		)
	}
	if written.CommandTimeoutSeconds == nil || *written.CommandTimeoutSeconds != 120 {
		t.Errorf("expected CommandTimeoutSeconds 120, got %v", written.CommandTimeoutSeconds)
	}
}

func TestRunUpdate_OmittedTuningFlagsPreserveExistingValues(t *testing.T) {
	var written agent.Device
	params := newBaseUpdateParams()
	params.FS = captureUpdateFS(deviceWithTuningJSON("test-org", 30, 8, 128, 4, 3, 90), &written)
	// Only overwrite one field; the others must be preserved as-is.
	params.Tuning = tuningFlags{
		MqttConnectTimeoutSeconds:       tuningFlagUnset,
		MqttSubscribeTimeoutSeconds:     tuningFlagUnset,
		WorkerCount:                     15,
		MessageQueueSize:                tuningFlagUnset,
		PostbackMaxAttempts:             tuningFlagUnset,
		PostbackBaseRetryBackoffSeconds: tuningFlagUnset,
		CommandTimeoutSeconds:           tuningFlagUnset,
	}

	runUpdate(params)

	if written.WorkerCount == nil || *written.WorkerCount != 15 {
		t.Errorf("expected WorkerCount overwritten to 15, got %v", written.WorkerCount)
	}
	if written.MqttConnectTimeoutSeconds == nil || *written.MqttConnectTimeoutSeconds != 30 {
		t.Errorf(
			"expected MqttConnectTimeoutSeconds preserved at 30, got %v",
			written.MqttConnectTimeoutSeconds,
		)
	}
	if written.MessageQueueSize == nil || *written.MessageQueueSize != 128 {
		t.Errorf("expected MessageQueueSize preserved at 128, got %v", written.MessageQueueSize)
	}
	if written.PostbackMaxAttempts == nil || *written.PostbackMaxAttempts != 4 {
		t.Errorf("expected PostbackMaxAttempts preserved at 4, got %v", written.PostbackMaxAttempts)
	}
	if written.PostbackBaseRetryBackoffSeconds == nil ||
		*written.PostbackBaseRetryBackoffSeconds != 3 {
		t.Errorf(
			"expected PostbackBaseRetryBackoffSeconds preserved at 3, got %v",
			written.PostbackBaseRetryBackoffSeconds,
		)
	}
	if written.CommandTimeoutSeconds == nil || *written.CommandTimeoutSeconds != 90 {
		t.Errorf(
			"expected CommandTimeoutSeconds preserved at 90, got %v",
			written.CommandTimeoutSeconds,
		)
	}
}

func TestRunUpdate_OpenFails(t *testing.T) {
	params := newBaseUpdateParams()
	params.ServiceManager = &mockServiceManager{openErr: errors.New("service not found")}

	runUpdate(params)
}

func TestRunUpdate_StopFails(t *testing.T) {
	// Active service, Stop fails → returns before the sleep.
	params := newBaseUpdateParams()
	params.ServiceManager = &mockServiceManager{
		openService: &mockService{isActive: true, stopErr: errors.New("stop failed")},
	}

	runUpdate(params)
}

// A stop that fails — a wedged service that never reaches Stopped — must abort
// the update with the installation untouched: the old process may still hold the
// executable open, and a half-updated install is unrecoverable without hands on
// the endpoint.
func TestRunUpdate_StopFails_LeavesInstallationUntouched(t *testing.T) {
	var writes []string
	params := newBaseUpdateParams()
	params.FS = &mockFileSystem{
		executableFunc: func() (string, error) { return "/fake/agent", nil },
		readFileFunc:   func(string) ([]byte, error) { return validDeviceJSON("test-org"), nil },
		writeFileFunc: func(name string, _ []byte, _ os.FileMode) error {
			writes = append(writes, name)
			return nil
		},
		mkdirAllFunc:  func(string) error { return nil },
		removeAllFunc: func(string) error { return nil },
	}
	svc := &mockService{
		isActive: true,
		stopErr: errors.New(
			"service rewst_agent_smith_test-org did not stop within 5m0s: " +
				"last observed state StopPending",
		),
	}
	params.ServiceManager = &mockServiceManager{openService: svc}

	runUpdate(params)

	if !svc.stopCalled {
		t.Error("expected Stop to be attempted")
	}
	if len(writes) != 0 {
		t.Errorf("expected no files written after a failed stop, got %v", writes)
	}
	if svc.startCalled {
		t.Error("expected the service not to be started after a failed stop")
	}
	if svc.deleteCalled {
		t.Error("expected the service registration to be left alone after a failed stop")
	}
}

// A process that never releases the executable must abort the update before
// anything is written, and the service must be brought back up rather than left
// stopped with the endpoint silently offline.
func TestRunUpdate_ProcessNeverExits_AbortsAndRestartsService(t *testing.T) {
	clock := newFakeClock()
	var writes []string
	params := newBaseUpdateParams()
	params.exitWait = clock.options(2*time.Minute, 250*time.Millisecond)
	params.FS = &mockFileSystem{
		executableFunc: func() (string, error) { return "/fake/agent", nil },
		readFileFunc:   func(string) ([]byte, error) { return validDeviceJSON("test-org"), nil },
		writeFileFunc: func(name string, _ []byte, _ os.FileMode) error {
			writes = append(writes, name)
			return nil
		},
		mkdirAllFunc:  func(string) error { return nil },
		removeAllFunc: func(string) error { return nil },
		// The old process holds its image open for as long as it lives.
		executableInUseFunc: func(string) (bool, error) { return true, nil },
	}
	svc := &mockService{isActive: true}
	params.ServiceManager = &mockServiceManager{openService: svc}

	runUpdate(params)

	if !svc.stopCalled {
		t.Error("expected the service to be stopped")
	}
	if len(writes) != 0 {
		t.Errorf("expected nothing written while the old process is alive, got %v", writes)
	}
	if svc.deleteCalled {
		t.Error("expected the service registration to be left alone")
	}
	if !svc.startCalled {
		t.Error("expected the service to be restarted so the endpoint does not stay offline")
	}
	if clock.slept < 2*time.Minute {
		t.Errorf("expected the full deadline to be waited out, waited %s", clock.slept)
	}
}

// The executable is replaced by writing a temporary file next to it and renaming
// it into place, so an interrupted update can never leave a truncated binary.
func TestRunUpdate_ReplacesExecutableAtomically(t *testing.T) {
	var writes []string
	var renames [][2]string
	params := newBaseUpdateParams()
	params.FS = &mockFileSystem{
		executableFunc: func() (string, error) { return "/fake/agent", nil },
		readFileFunc:   func(string) ([]byte, error) { return validDeviceJSON("test-org"), nil },
		writeFileFunc: func(name string, _ []byte, _ os.FileMode) error {
			writes = append(writes, name)
			return nil
		},
		renameFunc: func(oldPath string, newPath string) error {
			renames = append(renames, [2]string{oldPath, newPath})
			return nil
		},
		mkdirAllFunc:  func(string) error { return nil },
		removeAllFunc: func(string) error { return nil },
	}

	runUpdate(params)

	agentExecutablePath := agent.GetAgentExecutablePath("test-org")
	for _, name := range writes {
		if !strings.HasSuffix(name, ".new") {
			t.Errorf("expected every write to target a temporary file, got %q", name)
		}
		if name == agentExecutablePath {
			t.Errorf("expected the installed executable never to be written in place")
		}
	}

	committed := false
	for _, rename := range renames {
		if rename[1] == agentExecutablePath && rename[0] == agentExecutablePath+".new" {
			committed = true
		}
	}
	if !committed {
		t.Errorf(
			"expected the executable to be committed by renaming %s.new into place, got %v",
			agentExecutablePath,
			renames,
		)
	}
}

// A commit that fails — the sharing violation this ticket is about, or a full
// disk — must leave the endpoint running the old agent rather than stopped.
func TestRunUpdate_ExecutableCommitFails_RestartsService(t *testing.T) {
	params := newBaseUpdateParams()
	agentExecutablePath := agent.GetAgentExecutablePath("test-org")
	removedTemps := []string{}
	params.FS = &mockFileSystem{
		executableFunc: func() (string, error) { return "/fake/agent", nil },
		readFileFunc:   func(string) ([]byte, error) { return validDeviceJSON("test-org"), nil },
		writeFileFunc:  func(string, []byte, os.FileMode) error { return nil },
		renameFunc: func(oldPath string, _ string) error {
			if oldPath == agentExecutablePath+".new" {
				return errors.New("sharing violation")
			}
			return nil
		},
		removeFunc: func(name string) error {
			removedTemps = append(removedTemps, name)
			return nil
		},
		mkdirAllFunc:  func(string) error { return nil },
		removeAllFunc: func(string) error { return nil },
	}
	svc := &mockService{isActive: true}
	params.ServiceManager = &mockServiceManager{openService: svc}

	runUpdate(params)

	if !svc.startCalled {
		t.Error("expected the service to be restarted after a failed executable write")
	}
	if len(removedTemps) != 1 || removedTemps[0] != agentExecutablePath+".new" {
		t.Errorf("expected the uncommitted temporary file to be cleaned up, got %v", removedTemps)
	}
}

func TestRunUpdate_PathsDataError(t *testing.T) {
	params := newBaseUpdateParams()
	params.Sys = &mockSystemInfoProvider{hostPlatformErr: errors.New("platform error")}

	runUpdate(params)
}

func TestRunUpdate_ReadConfigFileFails(t *testing.T) {
	params := newBaseUpdateParams()
	params.FS = &mockFileSystem{
		readFileFunc:   func(string) ([]byte, error) { return nil, errors.New("read failed") },
		writeFileFunc:  func(string, []byte, os.FileMode) error { return nil },
		executableFunc: func() (string, error) { return "/fake/agent", nil },
		mkdirAllFunc:   func(string) error { return nil },
		removeAllFunc:  func(string) error { return nil },
	}

	runUpdate(params)
}

func TestRunUpdate_InvalidConfigJSON(t *testing.T) {
	params := newBaseUpdateParams()
	params.FS = &mockFileSystem{
		readFileFunc:   func(string) ([]byte, error) { return []byte("not-json"), nil },
		writeFileFunc:  func(string, []byte, os.FileMode) error { return nil },
		executableFunc: func() (string, error) { return "/fake/agent", nil },
		mkdirAllFunc:   func(string) error { return nil },
		removeAllFunc:  func(string) error { return nil },
	}

	runUpdate(params)
}

func TestRunUpdate_WriteConfigFileFails(t *testing.T) {
	params := newBaseUpdateParams()
	params.FS = &mockFileSystem{
		readFileFunc:   func(string) ([]byte, error) { return validDeviceJSON("test-org"), nil },
		writeFileFunc:  func(string, []byte, os.FileMode) error { return errors.New("write failed") },
		executableFunc: func() (string, error) { return "/fake/agent", nil },
		mkdirAllFunc:   func(string) error { return nil },
		removeAllFunc:  func(string) error { return nil },
	}

	runUpdate(params)
}

func TestRunUpdate_ExecutableFails(t *testing.T) {
	params := newBaseUpdateParams()
	params.FS = &mockFileSystem{
		readFileFunc:   func(string) ([]byte, error) { return validDeviceJSON("test-org"), nil },
		writeFileFunc:  func(string, []byte, os.FileMode) error { return nil },
		executableFunc: func() (string, error) { return "", errors.New("executable error") },
		mkdirAllFunc:   func(string) error { return nil },
		removeAllFunc:  func(string) error { return nil },
	}

	runUpdate(params)
}

func TestRunUpdate_ReadExecutableFails(t *testing.T) {
	params := newBaseUpdateParams()
	readCall := 0
	params.FS = &mockFileSystem{
		readFileFunc: func(string) ([]byte, error) {
			readCall++
			if readCall == 1 {
				return validDeviceJSON("test-org"), nil
			}
			return nil, errors.New("read failed")
		},
		writeFileFunc:  func(string, []byte, os.FileMode) error { return nil },
		executableFunc: func() (string, error) { return "/fake/agent", nil },
		mkdirAllFunc:   func(string) error { return nil },
		removeAllFunc:  func(string) error { return nil },
	}

	runUpdate(params)
}

func TestRunUpdate_WriteAgentExecutableFails(t *testing.T) {
	params := newBaseUpdateParams()
	writeCall := 0
	params.FS = &mockFileSystem{
		readFileFunc: func(string) ([]byte, error) { return validDeviceJSON("test-org"), nil },
		writeFileFunc: func(string, []byte, os.FileMode) error {
			writeCall++
			if writeCall == 2 {
				return errors.New("write failed")
			}
			return nil
		},
		executableFunc: func() (string, error) { return "/fake/agent", nil },
		mkdirAllFunc:   func(string) error { return nil },
		removeAllFunc:  func(string) error { return nil },
	}

	runUpdate(params)
}

func TestRunUpdate_StartFails(t *testing.T) {
	params := newBaseUpdateParams()
	params.ServiceManager = &mockServiceManager{
		openService: &mockService{isActive: false, startErr: errors.New("start failed")},
	}

	runUpdate(params)
}

func TestRunUpdate_NoServiceUsername_DoesNotRecreate(t *testing.T) {
	params := newBaseUpdateParams()
	mgr := &mockServiceManager{openService: &mockService{isActive: false}}
	params.ServiceManager = mgr

	runUpdate(params)

	if len(mgr.createCalls) != 0 {
		t.Errorf("expected no Create calls without ServiceUsername, got %d", len(mgr.createCalls))
	}
}

func TestRunUpdate_WithServiceUsername_RecreatesService(t *testing.T) {
	params := newBaseUpdateParams()
	params.ServiceUsername = "rewst"
	params.ServicePassword = "p@ss"
	mgr := &mockServiceManager{
		openService:   &mockService{isActive: false},
		createService: &mockService{},
	}
	params.ServiceManager = mgr

	runUpdate(params)

	if len(mgr.createCalls) != 1 {
		t.Fatalf("expected 1 Create call when ServiceUsername set, got %d", len(mgr.createCalls))
	}
	got := mgr.createCalls[0]
	if got.ServiceUsername != "rewst" {
		t.Errorf(
			"expected ServiceUsername %q in Create params, got %q",
			"rewst",
			got.ServiceUsername,
		)
	}
	if got.ServicePassword != "p@ss" {
		t.Errorf(
			"expected ServicePassword %q in Create params, got %q",
			"p@ss",
			got.ServicePassword,
		)
	}
}

func TestRunUpdate_WithServiceUsername_DeleteFails(t *testing.T) {
	params := newBaseUpdateParams()
	params.ServiceUsername = "rewst"
	params.ServiceManager = &mockServiceManager{
		openService: &mockService{isActive: false, deleteErr: errors.New("delete failed")},
	}

	runUpdate(params)
}

func TestRunUpdate_WithServiceUsername_CreateFails(t *testing.T) {
	params := newBaseUpdateParams()
	params.ServiceUsername = "rewst"
	params.ServiceManager = &mockServiceManager{
		openService: &mockService{isActive: false},
		createErr:   errors.New("create failed"),
	}

	runUpdate(params)
}
