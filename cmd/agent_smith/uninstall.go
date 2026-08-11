package main

import (
	"os"
	"runtime"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
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

	// Delete data directory
	dataDir := agent.GetDataDirectory(params.OrgId)
	err = params.FS.RemoveAll(dataDir)
	if err != nil {
		logger.Error("Failed to delete directory", "directory", dataDir, "error", err)
		return
	}
	logger.Info("Directory deleted", "directory", dataDir)

	// Delete program directory
	programDir := agent.GetProgramDirectory(params.OrgId)
	err = params.FS.RemoveAll(programDir)
	if err != nil {
		logger.Error("Failed to delete directory", "directory", programDir, "error", err)
		return
	}
	logger.Info("Directory deleted", "directory", programDir)

	// Delete scripts directory
	scriptsDir := agent.GetScriptsDirectory(params.OrgId)
	err = params.FS.RemoveAll(scriptsDir)
	if err != nil {
		logger.Error("Failed to delete directory", "directory", scriptsDir, "error", err)
		return
	}
	logger.Info("Directory deleted", "directory", scriptsDir)
}
