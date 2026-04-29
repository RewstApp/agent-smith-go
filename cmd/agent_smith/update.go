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

	// Stop the service if its running
	if svc.IsActive() {
		logger.Info("Stopping service", "service", name)
		err = svc.Stop()
		if err != nil {
			logger.Error("Failed to stop service", "service", name, "error", err)
			_ = svc.Close()
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
		_ = svc.Close()
		return
	}

	// Read and parse the config file
	configFilePath := pathsData.ConfigFilePath
	configFileBytes, err := params.FS.ReadFile(configFilePath)
	if err != nil {
		logger.Error("Failed to load config", "error", err)
		_ = svc.Close()
		return
	}

	// Decode the config file
	var device agent.Device
	err = json.Unmarshal(configFileBytes, &device)
	if err != nil {
		logger.Error("Failed to decode config", "error", err)
		_ = svc.Close()
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

	// Save the updated configuration file
	configBytes, err := json.MarshalIndent(device, "", "  ")
	if err != nil {
		logger.Error("Failed to print config file", "error", err)
		_ = svc.Close()
		return
	}

	err = params.FS.WriteFile(configFilePath, configBytes, utils.DefaultFileMod)
	if err != nil {
		logger.Error("Failed to save config", "error", err)
		_ = svc.Close()
		return
	}

	logger.Info("Configuration successfully updated", "path", configFilePath)

	// Copy the agent executable
	execFilePath, err := params.FS.Executable()
	if err != nil {
		logger.Error("Failed to get executable", "error", err)
		_ = svc.Close()
		return
	}

	execFileBytes, err := params.FS.ReadFile(execFilePath)
	if err != nil {
		logger.Error("Failed to read executable file", "error", err)
		_ = svc.Close()
		return
	}

	// Retry the executable write: on Windows the old process holds an exe lock
	// until it fully exits, which can happen just after the SCM reports Stopped.
	agentExecutablePath := pathsData.AgentExecutablePath
	const maxWriteAttempts = 10
	const writeRetryInterval = 3 * time.Second
	var writeErr error
	for attempt := range maxWriteAttempts {
		writeErr = params.FS.WriteFile(agentExecutablePath, execFileBytes, utils.DefaultExecutableFileMod)
		if writeErr == nil {
			break
		}
		if attempt < maxWriteAttempts-1 {
			logger.Info("Agent executable in use, retrying",
				"attempt", attempt+1, "of", maxWriteAttempts, "error", writeErr)
			time.Sleep(writeRetryInterval)
		}
	}
	if writeErr != nil {
		logger.Error("Failed to create agent executable", "error", writeErr)
		_ = svc.Close()
		return
	}

	logger.Info("Agent installed to", "path", agentExecutablePath)

	// When a new service username is requested, re-register the service under the
	// new account instead of starting the existing registration.
	if params.ServiceUsername != "" {
		logger.Info("Re-registering service with new account", "username", params.ServiceUsername)

		if err = svc.Delete(); err != nil {
			logger.Error("Failed to delete service for re-registration", "service", name, "error", err)
			_ = svc.Close()
			return
		}
		if err = svc.Close(); err != nil {
			logger.Error("Failed to close service handle", "error", err)
			return
		}

		logger.Info("Waiting for service executable to stop")
		time.Sleep(serviceExecutableTimeout)

		svc, err = params.ServiceManager.Create(service.AgentParams{
			Name:                name,
			AgentExecutablePath: agentExecutablePath,
			OrgId:               params.OrgId,
			ConfigFilePath:      configFilePath,
			LogFilePath:         agent.GetLogFilePath(params.OrgId),
			ScriptsDirectory:    agent.GetScriptsDirectory(params.OrgId),
			ServiceUsername:     params.ServiceUsername,
			ServicePassword:     params.ServicePassword,
		})
		if err != nil {
			logger.Error("Failed to re-create service", "service", name, "error", err)
			return
		}
		defer func() {
			if err := svc.Close(); err != nil {
				logger.Error("Failed to close service handle", "error", err)
			}
		}()

		logger.Info("Service re-registered", "service", name)
	} else {
		defer func() {
			if err := svc.Close(); err != nil {
				logger.Error("Failed to close service handle", "error", err)
			}
		}()
	}

	// Starting the service
	logger.Info("Starting service", "service", name)
	if err = svc.Start(); err != nil {
		logger.Error("Failed to start service", "service", name, "error", err)
		return
	}

	logger.Info("Service started")
}
