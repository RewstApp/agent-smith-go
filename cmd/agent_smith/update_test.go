package main

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

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
	}
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestRunUpdate_Success(t *testing.T) {
	runUpdate(newBaseUpdateParams())
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
