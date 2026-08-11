package main

import (
	"context"
	"encoding/json"
	"os"
	"runtime"

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

	// Track whether this run stopped the service and whether it got as far as
	// starting it again. Registered after the handle cleanup so it runs first
	// (deferred calls run last-in-first-out) and still has a usable handle.
	stopped := false
	completed := false
	defer func() {
		if completed || !stopped {
			return
		}
		if svc == nil {
			// The registration was removed on the way to re-registering the service
			// and could not be recreated. There is nothing left to start, so say so
			// plainly instead of leaving the endpoint quietly offline.
			logger.Error(
				"Update failed after the service registration was removed; the endpoint is offline until the agent is reinstalled",
				"service",
				name,
			)
			return
		}
		logger.Info("Restarting service after failed update", "service", name)
		if err := svc.Start(); err != nil {
			logger.Error(
				"Failed to restart service after failed update; the endpoint is offline",
				"service", name,
				"error", err,
			)
			return
		}
		logger.Info("Service restarted after failed update", "service", name)
	}()

	// Get installation paths data. This is read before the service is stopped so
	// the wait below knows which executable to watch.
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
	agentExecutablePath := pathsData.AgentExecutablePath

	// Stop the service if its running
	if svc.IsActive() {
		logger.Info("Stopping service", "service", name)
		err = svc.Stop()
		if err != nil {
			// Abort without touching the installation. The old process may still
			// be running and holding the agent executable open, so overwriting it
			// would either fail or leave a half-updated install behind. Leaving
			// everything in place keeps the endpoint recoverable: stop the wedged
			// process, start the service, and the next update succeeds.
			logger.Error("Failed to stop service", "service", name, "error", err)
			logger.Error(
				"Update aborted; existing installation left untouched",
				"service", name,
				"agent_executable", "not modified",
				"config_file", "not modified",
			)
			return
		}
		stopped = true
	}

	// Wait for the old process to actually exit. Replacing an executable that is
	// still running fails outright on Windows and races the old process on Linux
	// and macOS, so nothing is written until the process is observably gone. This
	// runs even when the service was already stopped, since a process left over
	// from a crashed or externally stopped service holds its image just as firmly.
	// It costs a single round of probes when the process is already gone.
	logger.Info(
		"Waiting for the agent process to exit",
		"service", name,
		"agent_executable", agentExecutablePath,
	)
	if err := waitForAgentProcessExit(
		logger,
		svc,
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
			"Update aborted; existing installation left untouched",
			"service", name,
			"agent_executable", "not modified",
			"config_file", "not modified",
		)
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
	if params.Tuning.MqttSubscribeTimeoutSeconds != tuningFlagUnset {
		device.MqttSubscribeTimeoutSeconds = tuningPtr(params.Tuning.MqttSubscribeTimeoutSeconds)
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
		device.PostbackBaseRetryBackoffSeconds = tuningPtr(
			params.Tuning.PostbackBaseRetryBackoffSeconds,
		)
	}
	if params.Tuning.CommandTimeoutSeconds != tuningFlagUnset {
		device.CommandTimeoutSeconds = tuningPtr(params.Tuning.CommandTimeoutSeconds)
	}
	if params.Tuning.SasTokenLifetimeHours != tuningFlagUnset {
		device.SasTokenLifetimeHours = tuningPtr(params.Tuning.SasTokenLifetimeHours)
	}
	if params.Tuning.MaxOutputBytes != tuningFlagUnset {
		device.MaxOutputBytes = tuningPtr(params.Tuning.MaxOutputBytes)
	}

	// Save the updated configuration file
	configBytes, err := json.MarshalIndent(device, "", "  ")
	if err != nil {
		logger.Error("Failed to print config file", "error", err)
		return
	}

	// Written atomically: a config file truncated by a failed write would leave
	// the service unable to start at all.
	err = writeFileAtomic(params.FS, configFilePath, configBytes, utils.DefaultFileMod)
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

	// Written atomically so a failure here leaves the installed binary exactly as
	// it was: the endpoint keeps running the old agent rather than a truncated
	// one that cannot start.
	err = writeFileAtomic(
		params.FS,
		agentExecutablePath,
		execFileBytes,
		utils.DefaultExecutableFileMod,
	)
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

		// Wait for the deleted registration to disappear rather than assuming a
		// fixed sleep covered it. If it somehow outlives the deadline, still try to
		// create the service: failing here would leave the endpoint with no
		// registration at all, and Create reports the real conflict if there is one.
		logger.Info("Waiting for the service registration to be removed", "service", name)
		if err := waitForServiceDeregistration(
			params.ServiceManager,
			name,
			params.exitWait,
		); err != nil {
			logger.Error(
				"Service registration outlived its deletion; re-registering anyway",
				"service", name,
				"error", err,
			)
		}

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

	// Starting the service. From here on the start attempt is itself the recovery,
	// so a failure is reported once rather than retried by the deferred restart.
	completed = true
	logger.Info("Starting service", "service", name)
	err = svc.Start()
	if err != nil {
		logger.Error(
			"Failed to start service; the endpoint is offline",
			"service", name,
			"error", err,
		)
		return
	}

	logger.Info("Service started")
}
