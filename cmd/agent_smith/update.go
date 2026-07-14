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

func runUpdate(params *updateContext) {
	logger := utils.ConfigureLogger("agent_smith", os.Stdout, utils.Default)

	// Show header
	logger.Info("Agent Smith started", "version", version.Version, "os", runtime.GOOS)

	// Open the service
	name := agent.GetServiceName(params.OrgId)
	svc, err := params.ServiceManager.Open(name)
	if err != nil {
		logger.Error("Failed to open service", "name", name, "error", err)
		return
	}
	defer func() {
		if svc == nil {
			return
		}
		if err := svc.Close(); err != nil {
			logger.Error("Failed to close service handle", "error", err)
		}
	}()

	// Stop the service if its running
	if svc.IsActive() {
		logger.Info("Stopping service", "service", name)
		err = svc.Stop()
		if err != nil {
			logger.Error("Failed to stop service", "service", name, "error", err)
			return
		}

		// Wait for some time for the service executable to clean up
		logger.Info("Waiting for service executable to stop")
		time.Sleep(serviceExecutableTimeout)
	}

	// Get installation paths data
	pathsData, err := agent.NewPathsData(
		context.Background(),
		params.OrgId,
		logger,
		params.Sys,
		params.Domain,
	)
	if err != nil {
		logger.Error("Failed to read paths", "error", err)
		return
	}

	// Read and parse the config file
	configFilePath := pathsData.ConfigFilePath
	configFileBytes, err := params.FS.ReadFile(configFilePath)
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
	device.DisableAutoUpdates = params.NoAutoUpdates
	device.GithubToken = params.GithubToken

	if params.MqttQos != -1 {
		qos := byte(params.MqttQos)
		device.MqttQos = &qos
	} else {
		device.MqttQos = nil
	}

	// Overwrite each tuning field only when the operator provided the flag. An
	// omitted flag leaves whatever was already in the config file unchanged so
	// existing overrides are never silently reset to the default.
	if params.Tuning.MqttConnectTimeoutSeconds != tuningFlagUnset {
		device.MqttConnectTimeoutSeconds = tuningPtr(params.Tuning.MqttConnectTimeoutSeconds)
	}
	if params.Tuning.WorkerCount != tuningFlagUnset {
		device.WorkerCount = tuningPtr(params.Tuning.WorkerCount)
	}
	if params.Tuning.MessageQueueSize != tuningFlagUnset {
		device.MessageQueueSize = tuningPtr(params.Tuning.MessageQueueSize)
	}
	if params.Tuning.PostbackMaxAttempts != tuningFlagUnset {
		device.PostbackMaxAttempts = tuningPtr(params.Tuning.PostbackMaxAttempts)
	}
	if params.Tuning.PostbackBaseRetryBackoffSeconds != tuningFlagUnset {
		device.PostbackBaseRetryBackoffSeconds = tuningPtr(params.Tuning.PostbackBaseRetryBackoffSeconds)
	}

	// Save the updated configuration file
	configBytes, err := json.MarshalIndent(device, "", "  ")
	if err != nil {
		logger.Error("Failed to print config file", "error", err)
		return
	}

	err = params.FS.WriteFile(configFilePath, configBytes, utils.DefaultFileMod)
	if err != nil {
		logger.Error("Failed to save config", "error", err)
		return
	}

	logger.Info("Configuration successfully updated", "path", configFilePath)

	// Copy the agent executable
	execFilePath, err := params.FS.Executable()
	if err != nil {
		logger.Error("Failed to get executable", "error", err)
		return
	}

	execFileBytes, err := params.FS.ReadFile(execFilePath)
	if err != nil {
		logger.Error("Failed to read executable file", "error", err)
		return
	}

	agentExecutablePath := pathsData.AgentExecutablePath
	err = params.FS.WriteFile(agentExecutablePath, execFileBytes, utils.DefaultExecutableFileMod)
	if err != nil {
		logger.Error("Failed to create agent executable", "error", err)
		return
	}

	logger.Info("Agent installed to", "path", agentExecutablePath)

	// If service credentials were provided, re-register the service so the
	// new account takes effect. Otherwise just restart the existing
	// registration.
	if params.ServiceUsername != "" {
		logger.Info(
			"Re-registering service with new account",
			"service",
			name,
			"user",
			params.ServiceUsername,
		)

		if err := svc.Delete(); err != nil {
			logger.Error("Failed to delete service", "service", name, "error", err)
			return
		}
		if err := svc.Close(); err != nil {
			logger.Error("Failed to close service handle", "error", err)
		}
		svc = nil

		logger.Info("Waiting for service executable to stop")
		time.Sleep(serviceExecutableTimeout)

		newSvc, err := params.ServiceManager.Create(service.AgentParams{
			Name:                name,
			AgentExecutablePath: agentExecutablePath,
			OrgId:               params.OrgId,
			ConfigFilePath:      configFilePath,
			LogFilePath:         agent.GetLogFilePath(params.OrgId),
			ServiceUsername:     params.ServiceUsername,
			ServicePassword:     params.ServicePassword,
		})
		if err != nil {
			logger.Error("Failed to create service", "service", name, "error", err)
			return
		}
		svc = newSvc
		logger.Info("Service re-registered", "service", name)
	}

	// Starting the service
	logger.Info("Starting service", "service", name)
	err = svc.Start()
	if err != nil {
		logger.Error("Failed to start service", "service", name, "error", err)
		return
	}

	logger.Info("Service started")
}
