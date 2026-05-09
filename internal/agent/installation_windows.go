//go:build windows

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ValidateInstallationEnvironment checks that the Windows environment
// variables used to build installation paths are set. On stripped or
// misconfigured systems (e.g. Server Core, locked-down GPO, containers)
// these may be empty, which would otherwise produce malformed paths like
// `\RewstRemoteAgent\<orgId>` instead of `C:\Program Files\RewstRemoteAgent\<orgId>`.
func ValidateInstallationEnvironment() error {
	required := []string{"PROGRAMFILES", "PROGRAMDATA", "SYSTEMDRIVE"}
	var missing []string
	for _, name := range required {
		if os.Getenv(name) == "" {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"required Windows environment variable(s) not set: %s",
			strings.Join(missing, ", "),
		)
	}
	return nil
}

func GetProgramDirectory(orgId string) string {
	// Get program files directory
	programFilesDir := os.Getenv("PROGRAMFILES")

	// Build the program directory based on organization id
	return filepath.Join(programFilesDir, fmt.Sprintf("RewstRemoteAgent/%s", orgId))
}

func GetDataDirectory(orgId string) string {
	// Get program data directory
	programDataDir := os.Getenv("PROGRAMDATA")

	// Build the program directory based on organization id
	return filepath.Join(programDataDir, fmt.Sprintf("RewstRemoteAgent/%s", orgId))
}

func GetScriptsDirectory(orgId string) string {
	// Get program files directory
	systemDrive := os.Getenv("SYSTEMDRIVE")

	// Build the program directory based on organization id
	return filepath.Join(
		fmt.Sprintf("%s\\", systemDrive),
		fmt.Sprintf("RewstRemoteAgent/scripts/%s", orgId),
	)
}

func GetAgentExecutablePath(orgId string) string {
	return filepath.Join(GetProgramDirectory(orgId), "agent_smith.win.exe")
}

func GetServiceExecutablePath(orgId string) string {
	return GetAgentExecutablePath(orgId)
}

func GetServiceManagerPath(orgId string) string {
	return GetAgentExecutablePath(orgId)
}

func GetConfigFilePath(orgId string) string {
	return filepath.Join(GetDataDirectory(orgId), "config.json")
}

func GetLogFilePath(orgId string) string {
	return filepath.Join(GetDataDirectory(orgId), "rewst_agent.log")
}

func GetServiceName(orgId string) string {
	return fmt.Sprintf("RewstRemoteAgent_%s", orgId)
}
