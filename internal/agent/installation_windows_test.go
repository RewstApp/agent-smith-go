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

func TestValidateInstallationEnvironment_AllSet(t *testing.T) {
	setEnvVars(t)

	if err := ValidateInstallationEnvironment(); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestValidateInstallationEnvironment_MissingSingle(t *testing.T) {
	setEnvVars(t)
	t.Setenv("PROGRAMFILES", "")

	err := ValidateInstallationEnvironment()
	if err == nil {
		t.Fatal("expected error for missing PROGRAMFILES, got nil")
	}
	if !strings.Contains(err.Error(), "PROGRAMFILES") {
		t.Errorf("expected error to identify PROGRAMFILES, got %q", err.Error())
	}
}

func TestValidateInstallationEnvironment_MissingMultiple(t *testing.T) {
	setEnvVars(t)
	t.Setenv("PROGRAMFILES", "")
	t.Setenv("SYSTEMDRIVE", "")

	err := ValidateInstallationEnvironment()
	if err == nil {
		t.Fatal("expected error for missing variables, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "PROGRAMFILES") || !strings.Contains(msg, "SYSTEMDRIVE") {
		t.Errorf("expected error to identify PROGRAMFILES and SYSTEMDRIVE, got %q", msg)
	}
	if strings.Contains(msg, "PROGRAMDATA") {
		t.Errorf("did not expect PROGRAMDATA in error, got %q", msg)
	}
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
