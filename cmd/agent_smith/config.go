package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
)

type fetchConfigurationResponse struct {
	Configuration agent.Device `json:"configuration"`
}

func runConfig(params *configParams) {
	logger := utils.ConfigureLogger("agent_smith", os.Stdout, utils.Info)

	// Show header
	logger.Info("Agent Smith started", "version", version.Version, "os", runtime.GOOS)

	// Get installation paths data
	var pathsData agent.PathsData
	err := pathsData.Load(context.Background(), params.OrgId, logger)
	if err != nil {
		logger.Error("Failed to read paths", "error", err)
		return
	}

	// Fetch configuration
	hostInfoBytes, err := json.MarshalIndent(pathsData.Tags, "", "  ")
	if err != nil {
		logger.Error("Failed to read host info", "error", err)
		return
	}

	// Prepare http request and send
	logger.Info("Sending", "data", string(hostInfoBytes), "to", params.ConfigUrl)
	req, err := http.NewRequestWithContext(context.Background(), "POST", params.ConfigUrl, bytes.NewReader(hostInfoBytes))
	if err != nil {
		logger.Error("Failed to create request", "error", err)
		return
	}
	req.Header.Set("x-rewst-secret", params.ConfigSecret)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil {
		logger.Error("Failed to execute http request", "error", err)
		return
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		logger.Error("Failed to fetch configuration", "status_code", res.StatusCode)
		return
	}
	logger.Info("Successfully fetched configuration", "status_code", res.StatusCode)

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		logger.Error("Failed to read response", "error", err)
		return
	}

	// Parse the fetch configuration response
	var response fetchConfigurationResponse
	err = json.Unmarshal(bodyBytes, &response)
	if err != nil {
		logger.Error("Failed to parse response", "error", err)
		return
	}

	// Create the data directory
	dataDir := agent.GetDataDirectory(params.OrgId)
	err = utils.CreateFolderIfMissing(dataDir)
	if err != nil {
		logger.Error("Failed to create data directory", "error", err)
		return
	}

	// Save the configuration file
	configFilePath := agent.GetConfigFilePath(params.OrgId)
	configBytes, err := json.MarshalIndent(response.Configuration, "", "  ")
	if err != nil {
		logger.Error("Failed to print config file", "error", err)
		return
	}

	// Got configuration
	logger.Info("Received configuration", "configuration", string(configBytes))

	err = os.WriteFile(configFilePath, configBytes, utils.DefaultFileMod)
	if err != nil {
		logger.Error("Failed to save config", "error", err)
		return
	}

	name := agent.GetServiceName(params.OrgId)

	// Stop and delete the service if it already exists
	existingService, err := service.Open(name)
	if err == nil {
		if existingService.IsActive() {
			logger.Info("Stopping service", "service", name)
			err = existingService.Stop()
			if err != nil {
				logger.Error("Failed to stop service", "service", err)
				existingService.Close()
				return
			}
		}

		// Delete the service
		err = existingService.Delete()
		if err != nil {
			logger.Error("Failed to delete service", "error", err)
			return
		}
		logger.Info("Service deleted", "service", name)

		// Wait for some time for the service executable to clean up
		existingService.Close()
		logger.Info("Waiting for service executable to stop")
		time.Sleep(serviceExecutableTimeout)
	}

	logger.Info("Configuration saved to", "path", configFilePath)
	logger.Info("Logs will be saved to", "path", agent.GetLogFilePath(params.OrgId))

	// Create the program directory
	programDir := agent.GetProgramDirectory(params.OrgId)
	err = utils.CreateFolderIfMissing(programDir)
	if err != nil {
		logger.Error("Failed to create program directory", "error", err)
		return
	}

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

	agentExecutablePath := agent.GetAgentExecutablePath(params.OrgId)
	err = os.WriteFile(agentExecutablePath, execFileBytes, utils.DefaultExecutableFileMod)
	if err != nil {
		logger.Error("Failed to create agent executable", "error", err)
		return
	}

	logger.Info("Agent installed to", "path", agentExecutablePath)
	logger.Info("Commands will be temporarily saved to", "path", agent.GetScriptsDirectory(params.OrgId))

	// Create the service
	logger.Info("Creating service", "service", name)

	svc, err := service.Create(service.AgentParams{
		Name:                name,
		AgentExecutablePath: agentExecutablePath,
		OrgId:               params.OrgId,
		ConfigFilePath:      configFilePath,
		LogFilePath:         agent.GetLogFilePath(params.OrgId),
	})
	if err != nil {
		logger.Error("Failed to create service", "error", err)
		return
	}
	defer svc.Close()
	logger.Info("Service created")

	// Start the service
	logger.Info("Starting service", "service", name)
	err = svc.Start()
	if err != nil {
		logger.Error("Failed to start service", "service", err)
		return
	}

	logger.Info("Service started")
}
