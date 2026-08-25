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

// GetScriptsDirectory returns the directory command scripts are written to
// before execution.
//
// Unlike Linux and macOS (see GetScriptsDirectory in installation_linux.go /
// installation_darwin.go), this deliberately stays at its historical location
// — the root of the system drive, not under the agent's ProgramData data
// directory — for two reasons. First, some customers have their endpoint
// security software configured to whitelist exactly this path so the
// dynamically-written PowerShell scripts the agent executes here are not
// flagged or blocked as they run; moving it would silently break command
// execution on those endpoints with no obvious error pointing at the real
// cause. Second, the vulnerability the Linux/macOS move fixes (sc-108848) —
// an unprivileged local user pre-creating the directory with permissive
// ownership before the agent ever runs — depended on a shared, world-writable
// system temp directory. Windows never routed this path through anything
// like that: an unprivileged user cannot create a new top-level directory at
// the system drive root under Windows' default ACLs, so this location was
// never reachable by that attack to begin with. EnsureSecureDir still runs
// before every command and refuses a symlink or non-directory planted here,
// same as on Linux/macOS; see the README's "Hardening the Command Scripts
// Directory" section.
func GetScriptsDirectory(orgId string) string {
	if scriptsDirOverride != nil {
		return scriptsDirOverride(orgId)
	}

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
