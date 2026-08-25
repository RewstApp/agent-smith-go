//go:build linux

package agent

import (
	"fmt"
	"path/filepath"
)

// ValidateInstallationEnvironment is a no-op on Linux; installation paths
// are derived from fixed locations rather than environment variables.
func ValidateInstallationEnvironment() error {
	return nil
}

func GetProgramDirectory(orgId string) string {
	// Get program files directory
	programFilesDir := "/usr/local/bin"

	// Build the program directory based on organization id
	return filepath.Join(programFilesDir, fmt.Sprintf("rewst_remote_agent/%s", orgId))
}

func GetDataDirectory(orgId string) string {
	// Get program data directory
	programDataDir := "/etc/"

	// Build the program directory based on organization id
	return filepath.Join(programDataDir, fmt.Sprintf("rewst_remote_agent/%s", orgId))
}

// GetScriptsDirectory returns the directory command scripts are written to
// before execution: a subdirectory of the org's agent-owned data directory,
// matching GetUpdatesDirectory's precedent (see internal/agent/updater.go).
//
// This used to be a subdirectory of the shared, world-writable system temp
// directory (os.TempDir()), which let any local unprivileged user pre-create
// it — or wait for a tmpfs /tmp to reset on reboot — with permissive
// ownership and mode before the agent ever ran, and reuse it undetected since
// directory creation is a no-op when the directory already exists. Landing
// under the data directory instead means the directory the agent writes
// scripts into, and executes them from, is one only the agent's own
// privileged account can create in the first place. See EnsureSecureDir,
// which additionally re-asserts ownership and mode on every use rather than
// trusting a pre-existing directory, and the README's "Hardening the Command
// Scripts Directory" section.
//
// Windows deliberately does not follow this move — see GetScriptsDirectory in
// installation_windows.go.
func GetScriptsDirectory(orgId string) string {
	if scriptsDirOverride != nil {
		return scriptsDirOverride(orgId)
	}
	return filepath.Join(GetDataDirectory(orgId), "scripts")
}

func GetAgentExecutablePath(orgId string) string {
	return filepath.Join(GetProgramDirectory(orgId), "agent_smith.linux.bin")
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
	return fmt.Sprintf("rewst_remote_agent_%s", orgId)
}
