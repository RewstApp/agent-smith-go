//go:build windows

package agent

import (
	"fmt"
	"os"
	"path/filepath"
)

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
	return filepath.Join(fmt.Sprintf("%s\\", systemDrive), fmt.Sprintf("RewstRemoteAgent/scripts/%s", orgId))
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
