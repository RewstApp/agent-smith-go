package main

import (
	"os"
	"runtime"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
)

const serviceExecutableTimeout = time.Second * 5

func runUninstall(params *uninstallParams) {
	logger := utils.ConfigureLogger("agent_smith", os.Stdout, utils.Default)

	// Show header
	logger.Info("Agent Smith started", "version", version.Version, "os", runtime.GOOS)

	name := agent.GetServiceName(params.OrgId)

	service, err := service.Open(name)
	if err != nil {
		logger.Error("Failed to open service", "service", name, "error", err)
		return
	}
	defer service.Close()

	if service.IsActive() {
		logger.Info("Stopping service", "service", name)
		err = service.Stop()
		if err != nil {
			logger.Error("Failed to stop service", "error", err)
			return
		}

		logger.Info("Service stopped", "service", name)
	}

	// Delete the service
	err = service.Delete()
	if err != nil {
		logger.Error("Failed to delete service", "error", err)
		return
	}
	logger.Info("Service deleted", "service", name)

	// Wait for some time for the service executable to clean up
	logger.Info("Waiting for service executable to stop")
	time.Sleep(serviceExecutableTimeout)

	// Delete data directory
	dataDir := agent.GetDataDirectory(params.OrgId)
	err = os.RemoveAll(dataDir)
	if err != nil {
		logger.Error("Failed to delete directory", "directory", dataDir, "error", err)
		return
	}
	logger.Info("Directory deleted", "directory", dataDir)

	// Delete program directory
	programDir := agent.GetProgramDirectory(params.OrgId)
	err = os.RemoveAll(programDir)
	if err != nil {
		logger.Error("Failed to delete directory", "directory", programDir, "error", err)
		return
	}
	logger.Info("Directory deleted", "directory", programDir)

	// Delete scripts directory
	scriptsDir := agent.GetScriptsDirectory(params.OrgId)
	err = os.RemoveAll(scriptsDir)
	if err != nil {
		logger.Error("Failed to delete directory", "directory", scriptsDir, "error", err)
		return
	}
	logger.Info("Directory deleted", "directory", scriptsDir)
}
