package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
)

// ── helpers ──────────────────────────────────────────────────────────────────

func newConfigTestSys() *mockSystemInfoProvider {
	return &mockSystemInfoProvider{
		hostname:         "test-host",
		hostPlatform:     "windows",
		cpuModelName:     "Intel Core i9",
		totalMemoryBytes: 16 << 30,
	}
}

func newConfigTestFS() *mockFileSystem {
	return &mockFileSystem{
		executableFunc: func() (string, error) { return "/fake/agent", nil },
		readFileFunc:   func(string) ([]byte, error) { return []byte("binary"), nil },
		writeFileFunc:  func(string, []byte, os.FileMode) error { return nil },
		mkdirAllFunc:   func(string) error { return nil },
		removeAllFunc:  func(string) error { return nil },
	}
}

func newConfigTestServiceManager() *mockServiceManager {
	return &mockServiceManager{
		openErr:       errors.New("no existing service"),
		createService: &mockService{},
	}
}

func validConfigResponseBody(orgId string) string {
	resp := fetchConfigurationResponse{
		Configuration: agent.Device{
			DeviceId:        "device-123",
			RewstOrgId:      orgId,
			RewstEngineHost: "engine.example.com",
			SharedAccessKey: "key123",
			AzureIotHubHost: "hub.example.com",
		},
	}
	b, _ := json.Marshal(resp)
	return string(b)
}

func newConfigServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		w.Write([]byte(body)) //nolint:errcheck
	}))
}

func newBaseConfigParams(configURL string) *configContext {
	return &configContext{
		OrgId:          "test-org",
		ConfigUrl:      configURL,
		ConfigSecret:   "secret",
		LoggingLevel:   string(utils.Default),
		Sys:            newConfigTestSys(),
		Domain:         &mockDomainInfoProvider{},
		FS:             newConfigTestFS(),
		ServiceManager: newConfigTestServiceManager(),
	}
}

// ── validateConfiguration tests ──────────────────────────────────────────────

func TestValidateConfiguration_Valid(t *testing.T) {
	device := agent.Device{
		DeviceId:        "device-123",
		RewstEngineHost: "engine.example.com",
		SharedAccessKey: "key123",
		AzureIotHubHost: "hub.example.com",
	}
	if err := validateConfiguration(device); err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestValidateConfiguration_MissingFields(t *testing.T) {
	tests := []struct {
		name   string
		device agent.Device
		field  string
	}{
		{
			name: "missing device_id",
			device: agent.Device{
				RewstEngineHost: "engine.example.com",
				SharedAccessKey: "key123",
				AzureIotHubHost: "hub.example.com",
			},
			field: "device_id",
		},
		{
			name: "missing rewst_engine_host",
			device: agent.Device{
				DeviceId:        "device-123",
				SharedAccessKey: "key123",
				AzureIotHubHost: "hub.example.com",
			},
			field: "rewst_engine_host",
		},
		{
			name: "missing shared_access_key",
			device: agent.Device{
				DeviceId:        "device-123",
				RewstEngineHost: "engine.example.com",
				AzureIotHubHost: "hub.example.com",
			},
			field: "shared_access_key",
		},
		{
			name: "missing azure_iot_hub_host",
			device: agent.Device{
				DeviceId:        "device-123",
				RewstEngineHost: "engine.example.com",
				SharedAccessKey: "key123",
			},
			field: "azure_iot_hub_host",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfiguration(tt.device)
			if err == nil {
				t.Errorf("expected error for missing %s, got nil", tt.field)
				return
			}
			if !strings.Contains(err.Error(), tt.field) {
				t.Errorf("expected error to mention %q, got %v", tt.field, err)
			}
		})
	}
}

func TestRunConfig_InvalidConfiguration(t *testing.T) {
	// Response missing required fields (no device_id, engine host, etc.)
	body := `{"configuration": {}}`
	srv := newConfigServer(t, http.StatusOK, body)
	defer srv.Close()

	err := runConfig(newBaseConfigParams(srv.URL))

	if err == nil || !strings.Contains(err.Error(), "invalid configuration") {
		t.Errorf("expected 'invalid configuration' error, got %v", err)
	}
}

// ── tests ─────────────────────────────────────────────────────────────────────

func TestRunConfig_Success(t *testing.T) {
	srv := newConfigServer(t, http.StatusOK, validConfigResponseBody("test-org"))
	defer srv.Close()

	err := runConfig(newBaseConfigParams(srv.URL))
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestRunConfig_PathsDataError(t *testing.T) {
	srv := newConfigServer(t, http.StatusOK, validConfigResponseBody("test-org"))
	defer srv.Close()

	params := newBaseConfigParams(srv.URL)
	params.Sys = &mockSystemInfoProvider{hostPlatformErr: errors.New("platform error")}

	err := runConfig(params)

	if err == nil || !strings.Contains(err.Error(), "failed to read paths") {
		t.Errorf("expected 'failed to read paths' error, got %v", err)
	}
}

func TestRunConfig_HTTPRequestFails(t *testing.T) {
	// Close the server before the request so the connection is refused.
	srv := newConfigServer(t, http.StatusOK, "")
	url := srv.URL
	srv.Close()

	err := runConfig(newBaseConfigParams(url))

	if err == nil || !strings.Contains(err.Error(), "failed to execute http request") {
		t.Errorf("expected 'failed to execute http request' error, got %v", err)
	}
}

func TestRunConfig_HTTPNon200(t *testing.T) {
	srv := newConfigServer(t, http.StatusServiceUnavailable, "")
	defer srv.Close()

	err := runConfig(newBaseConfigParams(srv.URL))

	if err == nil || !strings.Contains(err.Error(), "failed to fetch configuration") {
		t.Errorf("expected 'failed to fetch configuration' error, got %v", err)
	}
}

func TestRunConfig_InvalidJSONResponse(t *testing.T) {
	srv := newConfigServer(t, http.StatusOK, "not-json")
	defer srv.Close()

	err := runConfig(newBaseConfigParams(srv.URL))

	if err == nil || !strings.Contains(err.Error(), "failed to parse response") {
		t.Errorf("expected 'failed to parse response' error, got %v", err)
	}
}

func TestRunConfig_MkdirAllDataDirFails(t *testing.T) {
	srv := newConfigServer(t, http.StatusOK, validConfigResponseBody("test-org"))
	defer srv.Close()

	params := newBaseConfigParams(srv.URL)
	params.FS = &mockFileSystem{
		mkdirAllFunc: func(string) error { return errors.New("mkdir failed") },
	}

	err := runConfig(params)

	if err == nil || !strings.Contains(err.Error(), "failed to create data directory") {
		t.Errorf("expected 'failed to create data directory' error, got %v", err)
	}
}

func TestRunConfig_WriteConfigFileFails(t *testing.T) {
	srv := newConfigServer(t, http.StatusOK, validConfigResponseBody("test-org"))
	defer srv.Close()

	params := newBaseConfigParams(srv.URL)
	params.FS = &mockFileSystem{
		mkdirAllFunc:  func(string) error { return nil },
		writeFileFunc: func(string, []byte, os.FileMode) error { return errors.New("write failed") },
	}

	err := runConfig(params)

	if err == nil || !strings.Contains(err.Error(), "failed to save config") {
		t.Errorf("expected 'failed to save config' error, got %v", err)
	}
}

func TestRunConfig_ExistingService_StopFails(t *testing.T) {
	srv := newConfigServer(t, http.StatusOK, validConfigResponseBody("test-org"))
	defer srv.Close()

	params := newBaseConfigParams(srv.URL)
	params.ServiceManager = &mockServiceManager{
		openService:   &mockService{isActive: true, stopErr: errors.New("stop failed")},
		createService: &mockService{},
	}

	err := runConfig(params)

	if err == nil || !strings.Contains(err.Error(), "failed to stop service") {
		t.Errorf("expected 'failed to stop service' error, got %v", err)
	}
}

func TestRunConfig_ExistingService_DeleteFails(t *testing.T) {
	srv := newConfigServer(t, http.StatusOK, validConfigResponseBody("test-org"))
	defer srv.Close()

	params := newBaseConfigParams(srv.URL)
	params.ServiceManager = &mockServiceManager{
		openService:   &mockService{isActive: false, deleteErr: errors.New("delete failed")},
		createService: &mockService{},
	}

	err := runConfig(params)

	if err == nil || !strings.Contains(err.Error(), "failed to delete service") {
		t.Errorf("expected 'failed to delete service' error, got %v", err)
	}
}

func TestRunConfig_MkdirAllProgramDirFails(t *testing.T) {
	srv := newConfigServer(t, http.StatusOK, validConfigResponseBody("test-org"))
	defer srv.Close()

	params := newBaseConfigParams(srv.URL)
	mkdirCount := 0
	params.FS = &mockFileSystem{
		mkdirAllFunc: func(string) error {
			mkdirCount++
			if mkdirCount == 2 {
				return errors.New("mkdir failed")
			}
			return nil
		},
		writeFileFunc: func(string, []byte, os.FileMode) error { return nil },
	}

	err := runConfig(params)

	if err == nil || !strings.Contains(err.Error(), "failed to create program directory") {
		t.Errorf("expected 'failed to create program directory' error, got %v", err)
	}
}

func TestRunConfig_ExecutableFails(t *testing.T) {
	srv := newConfigServer(t, http.StatusOK, validConfigResponseBody("test-org"))
	defer srv.Close()

	params := newBaseConfigParams(srv.URL)
	params.FS = &mockFileSystem{
		mkdirAllFunc:   func(string) error { return nil },
		writeFileFunc:  func(string, []byte, os.FileMode) error { return nil },
		executableFunc: func() (string, error) { return "", errors.New("executable error") },
	}

	err := runConfig(params)

	if err == nil || !strings.Contains(err.Error(), "failed to get executable") {
		t.Errorf("expected 'failed to get executable' error, got %v", err)
	}
}

func TestRunConfig_ReadFileFails(t *testing.T) {
	srv := newConfigServer(t, http.StatusOK, validConfigResponseBody("test-org"))
	defer srv.Close()

	params := newBaseConfigParams(srv.URL)
	params.FS = &mockFileSystem{
		mkdirAllFunc:   func(string) error { return nil },
		writeFileFunc:  func(string, []byte, os.FileMode) error { return nil },
		executableFunc: func() (string, error) { return "/fake/agent", nil },
		readFileFunc:   func(string) ([]byte, error) { return nil, errors.New("read failed") },
	}

	err := runConfig(params)

	if err == nil || !strings.Contains(err.Error(), "failed to read executable file") {
		t.Errorf("expected 'failed to read executable file' error, got %v", err)
	}
}

func TestRunConfig_WriteAgentExecutableFails(t *testing.T) {
	srv := newConfigServer(t, http.StatusOK, validConfigResponseBody("test-org"))
	defer srv.Close()

	params := newBaseConfigParams(srv.URL)
	writeCount := 0
	params.FS = &mockFileSystem{
		mkdirAllFunc: func(string) error { return nil },
		writeFileFunc: func(string, []byte, os.FileMode) error {
			writeCount++
			if writeCount == 2 {
				return errors.New("write failed")
			}
			return nil
		},
		executableFunc: func() (string, error) { return "/fake/agent", nil },
		readFileFunc:   func(string) ([]byte, error) { return []byte("binary"), nil },
	}

	err := runConfig(params)

	if err == nil || !strings.Contains(err.Error(), "failed to create agent executable") {
		t.Errorf("expected 'failed to create agent executable' error, got %v", err)
	}
}

func TestRunConfig_ServiceCreateFails(t *testing.T) {
	srv := newConfigServer(t, http.StatusOK, validConfigResponseBody("test-org"))
	defer srv.Close()

	params := newBaseConfigParams(srv.URL)
	params.ServiceManager = &mockServiceManager{
		openErr:   errors.New("no existing service"),
		createErr: errors.New("create failed"),
	}

	err := runConfig(params)

	if err == nil || !strings.Contains(err.Error(), "failed to create service") {
		t.Errorf("expected 'failed to create service' error, got %v", err)
	}
}

func TestRunConfig_ServiceStartFails(t *testing.T) {
	srv := newConfigServer(t, http.StatusOK, validConfigResponseBody("test-org"))
	defer srv.Close()

	params := newBaseConfigParams(srv.URL)
	params.ServiceManager = &mockServiceManager{
		openErr:       errors.New("no existing service"),
		createService: &mockService{startErr: errors.New("start failed")},
	}

	err := runConfig(params)

	if err == nil || !strings.Contains(err.Error(), "failed to start service") {
		t.Errorf("expected 'failed to start service' error, got %v", err)
	}
}

func TestRunConfig_HTTPTimeout(t *testing.T) {
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-done:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer close(done) // unblocks handler before srv.Close() drains connections

	params := newBaseConfigParams(srv.URL)
	params.HTTPClient = &http.Client{Timeout: 50 * time.Millisecond}

	err := runConfig(params)

	if err == nil || !strings.Contains(err.Error(), "failed to execute http request") {
		t.Errorf("expected 'failed to execute http request' error, got %v", err)
	}
}
