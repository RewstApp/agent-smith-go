//go:build linux

package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func setEnvVars(t *testing.T) {
	// Set environment variables specific to Linux
	t.Setenv("HOME", "/home/user")
}

func TestGetProgramDirectory(t *testing.T) {
	setEnvVars(t)

	orgId := "org123"
	expected := filepath.Join("/usr/local/bin", "rewst_remote_agent", orgId)

	result := GetProgramDirectory(orgId)
	if result != expected {
		t.Errorf("Expected %s, got %s", expected, result)
	}
}

func TestGetDataDirectory(t *testing.T) {
	setEnvVars(t)

	orgId := "org123"
	expected := filepath.Join("/etc", "rewst_remote_agent", orgId)

	result := GetDataDirectory(orgId)
	if result != expected {
		t.Errorf("Expected %s, got %s", expected, result)
	}
}

func TestGetScriptsDirectory(t *testing.T) {
	setEnvVars(t)

	orgId := "org123"
	expected := filepath.Join(os.TempDir(), "rewst_remote_agent/scripts", orgId)

	result := GetScriptsDirectory(orgId)
	if result != expected {
		t.Errorf("Expected %s, got %s", expected, result)
	}
}

func TestGetAgentExecutablePath(t *testing.T) {
	setEnvVars(t)

	orgId := "org123"
	expected := filepath.Join("/usr/local/bin", "rewst_remote_agent", orgId, "agent_smith.linux.bin")

	result := GetAgentExecutablePath(orgId)
	if result != expected {
		t.Errorf("Expected %s, got %s", expected, result)
	}
}

func TestGetConfigFilePath(t *testing.T) {
	setEnvVars(t)

	orgId := "org123"
	expected := filepath.Join("/etc", "rewst_remote_agent", orgId, "config.json")

	result := GetConfigFilePath(orgId)
	if result != expected {
		t.Errorf("Expected %s, got %s", expected, result)
	}
}

func TestGetLogFilePath(t *testing.T) {
	setEnvVars(t)

	orgId := "org123"
	expected := filepath.Join("/etc", "rewst_remote_agent", orgId, "rewst_agent.log")

	result := GetLogFilePath(orgId)
	if result != expected {
		t.Errorf("Expected %s, got %s", expected, result)
	}
}

func TestGetServiceName(t *testing.T) {
	orgId := "org123"
	expected := "rewst_remote_agent_" + orgId

	result := GetServiceName(orgId)
	if result != expected {
		t.Errorf("Expected %s, got %s", expected, result)
	}
}
