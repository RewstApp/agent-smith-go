//go:build windows

package agent

import (
	"path/filepath"
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
}

func TestGetDataDirectory(t *testing.T) {
	setEnvVars(t)

	orgId := "org123"
	expected := filepath.Join("C:\\ProgramData", "RewstRemoteAgent", orgId)

	result := GetDataDirectory(orgId)

	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
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
}

func TestGetAgentExecutablePath(t *testing.T) {
	setEnvVars(t)
	orgId := "org123"
	expected := filepath.Join("C:\\Program Files", "RewstRemoteAgent", orgId, "agent_smith.win.exe")

	result := GetAgentExecutablePath(orgId)

	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
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
}

func TestGetLogFilePath(t *testing.T) {
	setEnvVars(t)
	orgId := "org123"
	expected := filepath.Join("C:\\ProgramData", "RewstRemoteAgent", orgId, "rewst_agent.log")

	result := GetLogFilePath(orgId)

	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}
}

func TestGetServiceName(t *testing.T) {
	orgId := "org123"
	expected := "RewstRemoteAgent_" + orgId

	result := GetServiceName(orgId)

	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}
}
