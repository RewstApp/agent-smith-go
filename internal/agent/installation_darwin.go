//go:build darwin

package agent

import (
	"fmt"
	"os"
	"path/filepath"
)

func GetProgramDirectory(orgId string) string {
	// Get program files directory
	programFilesDir := "/usr/local/bin"

	// Build the program directory based on organization id
	return filepath.Join(programFilesDir, fmt.Sprintf("rewst_remote_agent/%s", orgId))
}

func GetDataDirectory(orgId string) string {
	// Get program data directory
	programDataDir := "/Library/Application Support"

	// Build the program directory based on organization id
	return filepath.Join(programDataDir, fmt.Sprintf("rewst_remote_agent/%s", orgId))
}

func GetScriptsDirectory(orgId string) string {
	// Get program files directory
	tempDir := os.TempDir()

	// Build the program directory based on organization id
	return filepath.Join(tempDir, fmt.Sprintf("rewst_remote_agent/scripts/%s", orgId))
}

func GetAgentExecutablePath(orgId string) string {
	return filepath.Join(GetProgramDirectory(orgId), "agent_smith.mac-os.bin")
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
	return fmt.Sprintf("io.rewst.remote_agent_%s", orgId)
}
