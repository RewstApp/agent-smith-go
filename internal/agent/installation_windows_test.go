//go:build windows

package agent

import (
	"path/filepath"
	"strings"
	"testing"
)

func setEnvVars(t *testing.T) {
	// Set the necessary environment variables for the test
	t.Setenv("PROGRAMFILES", "C:\\Program Files")
	t.Setenv("PROGRAMDATA", "C:\\ProgramData")
	t.Setenv("SYSTEMDRIVE", "C:")
}

func TestGetProgramDirectory(t *testing.T) {
	setEnvVars(t)

	orgId := "org123"
	expected := filepath.Join("C:\\Program Files", "RewstRemoteAgent", orgId)

	result := GetProgramDirectory(orgId)

	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}

	// Test with different org IDs
	testCases := []string{"org1", "org-with-dashes", "org_with_underscores", "123456"}
	for _, testOrgId := range testCases {
		t.Run(testOrgId, func(t *testing.T) {
			result := GetProgramDirectory(testOrgId)
			if !strings.Contains(result, testOrgId) {
				t.Errorf("expected directory to contain %s, got %s", testOrgId, result)
			}
			if !strings.Contains(result, "RewstRemoteAgent") {
				t.Errorf("expected directory to contain RewstRemoteAgent, got %s", result)
			}
		})
	}
}

func TestGetDataDirectory(t *testing.T) {
	setEnvVars(t)

	orgId := "org123"
	expected := filepath.Join("C:\\ProgramData", "RewstRemoteAgent", orgId)

	result := GetDataDirectory(orgId)

	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}

	// Test with different org IDs
	testCases := []string{"org1", "org-with-dashes", "org_with_underscores", "123456"}
	for _, testOrgId := range testCases {
		t.Run(testOrgId, func(t *testing.T) {
			result := GetDataDirectory(testOrgId)
			if !strings.Contains(result, testOrgId) {
				t.Errorf("expected directory to contain %s, got %s", testOrgId, result)
			}
			if !strings.Contains(result, "RewstRemoteAgent") {
				t.Errorf("expected directory to contain RewstRemoteAgent, got %s", result)
			}
		})
	}
}

func TestGetScriptsDirectory(t *testing.T) {
	setEnvVars(t)

	orgId := "org123"
	expected := filepath.Join("C:\\", "RewstRemoteAgent", "scripts", orgId)

	result := GetScriptsDirectory(orgId)

	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}

	// Verify scripts directory structure
	if !strings.Contains(result, "scripts") {
		t.Errorf("expected directory to contain 'scripts', got %s", result)
	}
}

func TestGetAgentExecutablePath(t *testing.T) {
	setEnvVars(t)
	orgId := "org123"
	expected := filepath.Join("C:\\Program Files", "RewstRemoteAgent", orgId, "agent_smith.win.exe")

	result := GetAgentExecutablePath(orgId)

	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}

	// Verify it matches program directory + executable name
	programDir := GetProgramDirectory(orgId)
	expectedPath := filepath.Join(programDir, "agent_smith.win.exe")
	if result != expectedPath {
		t.Errorf("expected %s (from GetProgramDirectory), got %s", expectedPath, result)
	}
}

func TestGetServiceExecutablePath(t *testing.T) {
	setEnvVars(t)
	orgId := "test-org-service"
	servicePath := GetServiceExecutablePath(orgId)
	agentPath := GetAgentExecutablePath(orgId)

	// Should return the same as GetAgentExecutablePath
	if servicePath != agentPath {
		t.Errorf("expected service path %s to equal agent path %s", servicePath, agentPath)
	}

	// Verify it contains the executable name
	if !strings.HasSuffix(servicePath, "agent_smith.win.exe") {
		t.Errorf("expected service path to end with agent_smith.win.exe, got %s", servicePath)
	}
}

func TestGetServiceManagerPath(t *testing.T) {
	setEnvVars(t)
	orgId := "test-org-manager"
	managerPath := GetServiceManagerPath(orgId)
	agentPath := GetAgentExecutablePath(orgId)

	// Should return the same as GetAgentExecutablePath
	if managerPath != agentPath {
		t.Errorf("expected manager path %s to equal agent path %s", managerPath, agentPath)
	}

	// Verify it contains the executable name
	if !strings.HasSuffix(managerPath, "agent_smith.win.exe") {
		t.Errorf("expected manager path to end with agent_smith.win.exe, got %s", managerPath)
	}
}

func TestGetConfigFilePath(t *testing.T) {
	setEnvVars(t)
	orgId := "org123"
	expected := filepath.Join("C:\\ProgramData", "RewstRemoteAgent", orgId, "config.json")

	result := GetConfigFilePath(orgId)

	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}

	// Should be in data directory
	dataDir := GetDataDirectory(orgId)
	expectedPath := filepath.Join(dataDir, "config.json")
	if result != expectedPath {
		t.Errorf("expected %s (from GetDataDirectory), got %s", expectedPath, result)
	}
}

func TestGetLogFilePath(t *testing.T) {
	setEnvVars(t)
	orgId := "org123"
	expected := filepath.Join("C:\\ProgramData", "RewstRemoteAgent", orgId, "rewst_agent.log")

	result := GetLogFilePath(orgId)

	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}

	// Should be in data directory
	dataDir := GetDataDirectory(orgId)
	expectedPath := filepath.Join(dataDir, "rewst_agent.log")
	if result != expectedPath {
		t.Errorf("expected %s (from GetDataDirectory), got %s", expectedPath, result)
	}
}

func TestGetServiceName(t *testing.T) {
	orgId := "org123"
	expected := "RewstRemoteAgent_" + orgId

	result := GetServiceName(orgId)

	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}

	// Test with different org IDs
	testCases := map[string]string{
		"org1":                 "RewstRemoteAgent_org1",
		"org-with-dashes":      "RewstRemoteAgent_org-with-dashes",
		"org_with_underscores": "RewstRemoteAgent_org_with_underscores",
		"123456":               "RewstRemoteAgent_123456",
	}
	for testOrgId, expectedName := range testCases {
		t.Run(testOrgId, func(t *testing.T) {
			result := GetServiceName(testOrgId)
			if result != expectedName {
				t.Errorf("expected %s, got %s", expectedName, result)
			}
		})
	}
}

// TestPathConsistency verifies that paths are consistent across different functions
func TestPathConsistency(t *testing.T) {
	setEnvVars(t)
	orgId := "consistency-test"

	programDir := GetProgramDirectory(orgId)
	dataDir := GetDataDirectory(orgId)

	// Executable paths should be in program directory
	agentPath := GetAgentExecutablePath(orgId)
	if !strings.HasPrefix(agentPath, programDir) {
		t.Errorf("agent path should be in program directory: %s not in %s", agentPath, programDir)
	}

	// Config and log should be in data directory
	configPath := GetConfigFilePath(orgId)
	if !strings.HasPrefix(configPath, dataDir) {
		t.Errorf("config path should be in data directory: %s not in %s", configPath, dataDir)
	}

	logPath := GetLogFilePath(orgId)
	if !strings.HasPrefix(logPath, dataDir) {
		t.Errorf("log path should be in data directory: %s not in %s", logPath, dataDir)
	}

	// All paths should contain the org ID
	paths := []string{programDir, dataDir, agentPath, configPath, logPath}
	for _, path := range paths {
		if !strings.Contains(path, orgId) {
			t.Errorf("path should contain org ID: %s", path)
		}
	}
}

// TestEmptyOrgId tests behavior with empty org ID
func TestEmptyOrgId(t *testing.T) {
	setEnvVars(t)

	// These should still return valid paths, just without the org ID portion
	paths := []struct {
		name string
		fn   func(string) string
	}{
		{"GetProgramDirectory", GetProgramDirectory},
		{"GetDataDirectory", GetDataDirectory},
		{"GetScriptsDirectory", GetScriptsDirectory},
		{"GetAgentExecutablePath", GetAgentExecutablePath},
		{"GetServiceExecutablePath", GetServiceExecutablePath},
		{"GetServiceManagerPath", GetServiceManagerPath},
		{"GetConfigFilePath", GetConfigFilePath},
		{"GetLogFilePath", GetLogFilePath},
		{"GetServiceName", GetServiceName},
	}

	for _, tc := range paths {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.fn("")
			if result == "" {
				t.Error("expected non-empty result even with empty org ID")
			}
		})
	}
}

// TestAllFunctionsReturnNonEmpty tests that all functions return non-empty strings
func TestAllFunctionsReturnNonEmpty(t *testing.T) {
	setEnvVars(t)
	orgId := "test-org"

	tests := []struct {
		name string
		fn   func(string) string
	}{
		{"GetProgramDirectory", GetProgramDirectory},
		{"GetDataDirectory", GetDataDirectory},
		{"GetScriptsDirectory", GetScriptsDirectory},
		{"GetAgentExecutablePath", GetAgentExecutablePath},
		{"GetServiceExecutablePath", GetServiceExecutablePath},
		{"GetServiceManagerPath", GetServiceManagerPath},
		{"GetConfigFilePath", GetConfigFilePath},
		{"GetLogFilePath", GetLogFilePath},
		{"GetServiceName", GetServiceName},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.fn(orgId)
			if result == "" {
				t.Errorf("%s returned empty string", tt.name)
			}
			if !strings.Contains(result, orgId) {
				t.Errorf("%s result should contain org ID %s, got %s", tt.name, orgId, result)
			}
		})
	}
}
