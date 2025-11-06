package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
)

func TestServiceParams_loadConfig(t *testing.T) {
	// Create a temporary directory for test files
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "config.json")

	// Create a test device configuration
	testDevice := agent.Device{
		DeviceId:        "test-device-123",
		RewstEngineHost: "example.com",
		RewstOrgId:      "test-org",
		LoggingLevel:    utils.Default,
	}

	// Write the test configuration to file
	configBytes, err := json.Marshal(testDevice)
	if err != nil {
		t.Fatalf("failed to marshal test device: %v", err)
	}

	err = os.WriteFile(configFile, configBytes, utils.DefaultFileMod)
	if err != nil {
		t.Fatalf("failed to write config file: %v", err)
	}

	// Create service params
	svc := &serviceParams{
		ConfigFile: configFile,
		LogFile:    filepath.Join(tmpDir, "test.log"),
		OrgId:      "test-org",
	}

	// Test loading config
	device, err := svc.loadConfig()
	if err != nil {
		t.Fatalf("loadConfig() error = %v", err)
	}

	if device.DeviceId != testDevice.DeviceId {
		t.Errorf("expected DeviceId %s, got %s", testDevice.DeviceId, device.DeviceId)
	}

	if device.RewstEngineHost != testDevice.RewstEngineHost {
		t.Errorf("expected RewstEngineHost %s, got %s", testDevice.RewstEngineHost, device.RewstEngineHost)
	}

	if device.RewstOrgId != testDevice.RewstOrgId {
		t.Errorf("expected RewstOrgId %s, got %s", testDevice.RewstOrgId, device.RewstOrgId)
	}
}

func TestServiceParams_loadConfig_FileNotFound(t *testing.T) {
	svc := &serviceParams{
		ConfigFile: "/nonexistent/path/config.json",
		LogFile:    "/tmp/test.log",
		OrgId:      "test-org",
	}

	_, err := svc.loadConfig()
	if err == nil {
		t.Error("expected error for nonexistent config file, got nil")
	}
}

func TestServiceParams_loadConfig_InvalidJSON(t *testing.T) {
	tmpDir := t.TempDir()
	configFile := filepath.Join(tmpDir, "invalid.json")

	// Write invalid JSON
	err := os.WriteFile(configFile, []byte("not valid json{"), utils.DefaultFileMod)
	if err != nil {
		t.Fatalf("failed to write invalid config file: %v", err)
	}

	svc := &serviceParams{
		ConfigFile: configFile,
		LogFile:    filepath.Join(tmpDir, "test.log"),
		OrgId:      "test-org",
	}

	_, err = svc.loadConfig()
	if err == nil {
		t.Error("expected error for invalid JSON, got nil")
	}
}

func TestServiceParams_loadLog(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "test.log")

	svc := &serviceParams{
		ConfigFile: filepath.Join(tmpDir, "config.json"),
		LogFile:    logFile,
		OrgId:      "test-org",
	}

	file, err := svc.loadLog()
	if err != nil {
		t.Fatalf("loadLog() error = %v", err)
	}
	defer file.Close()

	// Verify the file was created
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		t.Error("expected log file to be created")
	}

	// Verify we can write to the file
	_, err = file.WriteString("test log entry\n")
	if err != nil {
		t.Errorf("failed to write to log file: %v", err)
	}
}

func TestServiceParams_loadLog_InvalidPath(t *testing.T) {
	svc := &serviceParams{
		ConfigFile: "/tmp/config.json",
		LogFile:    "/invalid/path/that/does/not/exist/test.log",
		OrgId:      "test-org",
	}

	_, err := svc.loadLog()
	if err == nil {
		t.Error("expected error for invalid log file path, got nil")
	}
}

func TestServiceParams_Name(t *testing.T) {
	tests := []struct {
		name  string
		orgId string
	}{
		{
			name:  "standard org id",
			orgId: "test-org-123",
		},
		{
			name:  "org id with special chars",
			orgId: "org_special-123",
		},
		{
			name:  "short org id",
			orgId: "abc",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &serviceParams{
				ConfigFile: "/tmp/config.json",
				LogFile:    "/tmp/test.log",
				OrgId:      tt.orgId,
			}

			serviceName := svc.Name()
			expectedName := agent.GetServiceName(tt.orgId)

			if serviceName != expectedName {
				t.Errorf("Name() = %s, expected %s", serviceName, expectedName)
			}
		})
	}
}

func TestServiceParams_loadLog_ExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := filepath.Join(tmpDir, "existing.log")

	// Create the log file with some content
	initialContent := "existing log content\n"
	err := os.WriteFile(logFile, []byte(initialContent), utils.DefaultFileMod)
	if err != nil {
		t.Fatalf("failed to create existing log file: %v", err)
	}

	svc := &serviceParams{
		ConfigFile: filepath.Join(tmpDir, "config.json"),
		LogFile:    logFile,
		OrgId:      "test-org",
	}

	file, err := svc.loadLog()
	if err != nil {
		t.Fatalf("loadLog() error = %v", err)
	}
	defer file.Close()

	// Write new content
	newContent := "new log entry\n"
	_, err = file.WriteString(newContent)
	if err != nil {
		t.Errorf("failed to write to log file: %v", err)
	}

	// Close and read the file to verify append mode
	file.Close()
	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}

	fullContent := string(content)
	if fullContent != initialContent+newContent {
		t.Errorf("expected content %q, got %q", initialContent+newContent, fullContent)
	}
}
