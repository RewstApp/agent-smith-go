package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func runConfig(params *configContext) error {
	logger := utils.ConfigureLogger("agent_smith", os.Stdout, utils.Default)

	// Show header
	logger.Info("Agent Smith started", "version", version.Version, "os", runtime.GOOS)

	// Get installation paths data
	pathsData, err := agent.NewPathsData(context.Background(), params.OrgId, logger, params.Sys, params.Domain)
	if err != nil {
		return fmt.Errorf("failed to read paths: %w", err)
	}

	// Fetch configuration
	hostInfoBytes, err := json.MarshalIndent(pathsData.Tags, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to read host info: %w", err)
	}

	// Prepare http request and send
	logger.Info("Sending", "data", string(hostInfoBytes), "to", params.ConfigUrl)
	req, err := utils.NewRequest("POST", params.ConfigUrl, bytes.NewReader(hostInfoBytes))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("x-rewst-secret", params.ConfigSecret)
	req.Header.Set("Content-Type", "application/json")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute http request: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch configuration: status %d", res.StatusCode)
	}
	logger.Info("Successfully fetched configuration", "status_code", res.StatusCode)

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	// Parse the fetch configuration response
	var response fetchConfigurationResponse
	err = json.Unmarshal(bodyBytes, &response)
	if err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}
	response.Configuration.LoggingLevel = utils.LoggingLevel(params.LoggingLevel)
	response.Configuration.UseSyslog = params.UseSyslog
	response.Configuration.DisableAgentPostback = params.DisableAgentPostback
	response.Configuration.DisableAutoUpdates = params.NoAutoUpdates

	// Create the data directory
	dataDir := agent.GetDataDirectory(params.OrgId)
	err = params.FS.MkdirAll(dataDir)
	if err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	// Save the configuration file
	configFilePath := agent.GetConfigFilePath(params.OrgId)
	configBytes, err := json.MarshalIndent(response.Configuration, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to print config file: %w", err)
	}

	// Got configuration
	logger.Info("Received configuration", "configuration", string(configBytes))

	err = params.FS.WriteFile(configFilePath, configBytes, utils.DefaultFileMod)
	if err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	name := agent.GetServiceName(params.OrgId)

	// Stop and delete the service if it already exists
	existingService, err := params.ServiceManager.Open(name)
	if err == nil {
		if existingService.IsActive() {
			logger.Info("Stopping service", "service", name)
			err = existingService.Stop()
			if err != nil {
				existingService.Close()
				return fmt.Errorf("failed to stop service %s: %w", name, err)
			}
		}

		// Delete the service
		err = existingService.Delete()
		if err != nil {
			return fmt.Errorf("failed to delete service: %w", err)
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
	err = params.FS.MkdirAll(programDir)
	if err != nil {
		return fmt.Errorf("failed to create program directory: %w", err)
	}

	// Copy the agent executable
	execFilePath, err := params.FS.Executable()
	if err != nil {
		return fmt.Errorf("failed to get executable: %w", err)
	}

	execFileBytes, err := params.FS.ReadFile(execFilePath)
	if err != nil {
		return fmt.Errorf("failed to read executable file: %w", err)
	}

	agentExecutablePath := agent.GetAgentExecutablePath(params.OrgId)
	err = params.FS.WriteFile(agentExecutablePath, execFileBytes, utils.DefaultExecutableFileMod)
	if err != nil {
		return fmt.Errorf("failed to create agent executable: %w", err)
	}

	logger.Info("Agent installed to", "path", agentExecutablePath)
	logger.Info("Commands will be temporarily saved to", "path", agent.GetScriptsDirectory(params.OrgId))

	// Create the service
	logger.Info("Creating service", "service", name)

	svc, err := params.ServiceManager.Create(service.AgentParams{
		Name:                name,
		AgentExecutablePath: agentExecutablePath,
		OrgId:               params.OrgId,
		ConfigFilePath:      configFilePath,
		LogFilePath:         agent.GetLogFilePath(params.OrgId),
	})
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}
	defer svc.Close()
	logger.Info("Service created")

	// Start the service
	logger.Info("Starting service", "service", name)
	err = svc.Start()
	if err != nil {
		return fmt.Errorf("failed to start service %s: %w", name, err)
	}

	logger.Info("Service started")
	return nil
}
