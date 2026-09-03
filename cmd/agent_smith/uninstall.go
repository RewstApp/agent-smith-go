package main

import (
	"os"
	"runtime"
	"strings"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
	"github.com/hashicorp/go-hclog"
)

func runUninstall(params *uninstallContext) {
	logger := utils.ConfigureLogger("agent_smith", os.Stdout, utils.Default)

	// Show header
	logger.Info("Agent Smith started", "version", version.Version, "os", runtime.GOOS)

	name := agent.GetServiceName(params.OrgId)

	service, err := params.ServiceManager.Open(name)
	if err != nil {
		logger.Error("Failed to open service", "service", name, "error", err)
		return
	}
	defer func() {
		err = service.Close()
		if err != nil {
			logger.Error("Failed to close service handle", "error", err)
		}
	}()

	agentExecutablePath := agent.GetAgentExecutablePath(params.OrgId)

	if service.IsActive() {
		logger.Info("Stopping service", "service", name)
		err = service.Stop()
		if err != nil {
			// Abort before deleting anything. Removing the registration and the
			// files while the process is still live would leave a half-removed
			// installation that neither runs nor uninstalls cleanly.
			logger.Error("Failed to stop service", "service", name, "error", err)
			logger.Error(
				"Uninstall aborted; nothing was removed",
				"service", name,
				"service_registration", "intact",
				"installed_files", "intact",
			)
			return
		}

		logger.Info("Service stopped", "service", name)
	}

	// Wait for the old process to actually exit before removing anything. Files
	// deleted out from under a live process leave a partially removed install
	// that neither runs nor reinstalls cleanly, so the deletion only starts once
	// the process is observably gone.
	logger.Info(
		"Waiting for the agent process to exit",
		"service", name,
		"agent_executable", agentExecutablePath,
	)
	if err := waitForAgentProcessExit(
		logger,
		service,
		params.FS,
		agentExecutablePath,
		params.exitWait,
	); err != nil {
		logger.Error(
			"Agent process did not exit",
			"service", name,
			"agent_executable", agentExecutablePath,
			"error", err,
		)
		logger.Error(
			"Uninstall aborted; nothing was removed",
			"service", name,
			"service_registration", "intact",
			"installed_files", "intact",
		)
		return
	}

	// Delete the service
	err = service.Delete()
	if err != nil {
		logger.Error("Failed to delete service", "error", err)
		return
	}
	logger.Info("Service deleted", "service", name)

	// Remove the installed files. Every directory is attempted, and the paths
	// that could not be removed are reported together at the end.
	failed, attempted := removeInstallationDirectories(logger, params.FS, params.OrgId)
	if len(failed) > 0 {
		// Name every path that survived, so an operator can finish the cleanup
		// by hand without having to guess which directories were reached.
		logger.Error(
			"Uninstall incomplete; some directories could not be removed",
			"service", name,
			"service_registration", "removed",
			"failed_directories", strings.Join(failed, ", "),
			"failed_count", len(failed),
			"attempted_count", attempted,
		)
		return
	}

	logger.Info("Uninstall completed", "service", name)
}

// removeInstallationDirectories removes the org's data, program and scripts
// directories and returns the paths that could not be removed alongside the
// number attempted.
//
// Each directory is attempted independently of the others. Aborting on the first
// failure used to orphan the directories behind it — potentially tens of
// megabytes of data, program and script files — with the service registration
// already gone, so there was no clean path left to finish the job. A locked file
// in one directory (an AV scanner mid-scan, a stale open handle on Windows) is a
// routine occurrence during uninstall and must not decide whether the rest of
// the installation gets cleaned up.
//
// The order is data, program, scripts: on Linux and macOS the scripts directory
// is a subdirectory of the data directory, so removing the data directory
// normally takes it with it and the later attempt is a no-op (RemoveAll on a
// missing path succeeds). If the data directory removal failed, the separate
// attempt is a second, narrower chance to reclaim the scripts underneath it.
func removeInstallationDirectories(
	logger hclog.Logger,
	fsys utils.FileSystem,
	orgId string,
) (failed []string, attempted int) {
	directories := []string{
		agent.GetDataDirectory(orgId),
		agent.GetProgramDirectory(orgId),
		agent.GetScriptsDirectory(orgId),
	}

	for _, directory := range directories {
		if err := fsys.RemoveAll(directory); err != nil {
			logger.Error("Failed to delete directory", "directory", directory, "error", err)
			failed = append(failed, directory)
			continue
		}

		logger.Info("Directory deleted", "directory", directory)
	}

	return failed, len(directories)
}
