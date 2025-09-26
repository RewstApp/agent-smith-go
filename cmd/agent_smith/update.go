package main

import (
	"context"
	"encoding/json"
	"os"
	"runtime"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
)

func runUpdate(params *updateParams) {
	logger := utils.ConfigureLogger("agent_smith", os.Stdout, utils.Default)

	// Show header
	logger.Info("Agent Smith started", "version", version.Version, "os", runtime.GOOS)

	// Open the service
	name := agent.GetServiceName(params.OrgId)
	svc, err := service.Open(name)
	if err != nil {
		logger.Error("Failed to open service", "name", name)
		return
	}
	defer svc.Close()

	// Stop the service if its running
	if svc.IsActive() {
		logger.Info("Stopping service", "service", name)
		err = svc.Stop()
		if err != nil {
			logger.Error("Failed to stop service", "service", err)
			return
		}

		// Wait for some time for the service executable to clean up
		logger.Info("Waiting for service executable to stop")
		time.Sleep(serviceExecutableTimeout)
	}

	// Get installation paths data
	var pathsData agent.PathsData
	err = pathsData.Load(context.Background(), params.OrgId, logger)
	if err != nil {
		logger.Error("Failed to read paths", "error", err)
		return
	}

	// Read and parse the config file
	configFilePath := pathsData.ConfigFilePath
	configFileBytes, err := os.ReadFile(configFilePath)
	if err != nil {
		logger.Error("Failed to load config", "error", err)
		return
	}

	// Decode the config file
	var device agent.Device
	err = json.Unmarshal(configFileBytes, &device)
	if err != nil {
		logger.Error("Failed to decode config", "error", err)
		return
	}

	device.LoggingLevel = utils.LoggingLevel(params.LoggingLevel)
	device.UseSyslog = params.UseSyslog
	device.DisableAgentPostback = params.DisableAgentPostback

	// Save the updated configuration file
	configBytes, err := json.MarshalIndent(device, "", "  ")
	if err != nil {
		logger.Error("Failed to print config file", "error", err)
		return
	}

	err = os.WriteFile(configFilePath, configBytes, utils.DefaultFileMod)
	if err != nil {
		logger.Error("Failed to save config", "error", err)
		return
	}

	logger.Info("Configuration successfully updated", "path", configFilePath)

	// Copy the agent executable
	execFilePath, err := os.Executable()
	if err != nil {
		logger.Error("Failed to get executable", "error", err)
		return
	}

	execFileBytes, err := os.ReadFile(execFilePath)
	if err != nil {
		logger.Error("Failed to read executable file", "error", err)
		return
	}

	agentExecutablePath := pathsData.AgentExecutablePath
	err = os.WriteFile(agentExecutablePath, execFileBytes, utils.DefaultExecutableFileMod)
	if err != nil {
		logger.Error("Failed to create agent executable", "error", err)
		return
	}

	logger.Info("Agent installed to", "path", agentExecutablePath)

	// Starting the service
	logger.Info("Starting service", "service", name)
	err = svc.Start()
	if err != nil {
		logger.Error("Failed to start service", "service", err)
		return
	}

	logger.Info("Service started")
}
