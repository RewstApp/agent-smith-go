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

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
)

type fetchConfigurationResponse struct {
	Configuration agent.Device `json:"configuration"`
}

func validateConfiguration(device agent.Device) error {
	if device.DeviceId == "" {
		return fmt.Errorf("missing required field: device_id")
	}
	if device.RewstEngineHost == "" {
		return fmt.Errorf("missing required field: rewst_engine_host")
	}
	if device.SharedAccessKey == "" {
		return fmt.Errorf("missing required field: shared_access_key")
	}
	if device.AzureIotHubHost == "" {
		return fmt.Errorf("missing required field: azure_iot_hub_host")
	}
	return nil
}

func runConfig(params *configContext) error {
	logger := utils.ConfigureLogger("agent_smith", os.Stdout, utils.Default)

	// Show header
	logger.Info("Agent Smith started", "version", version.Version, "os", runtime.GOOS)

	// Get installation paths data
	pathsData, err := agent.NewPathsData(
		context.Background(),
		params.OrgId,
		logger,
		params.Sys,
		params.Domain,
	)
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

	httpClient := params.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: configHTTPTimeout}
	}
	res, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute http request: %w", err)
	}
	defer func() {
		err := res.Body.Close()
		if err != nil {
			logger.Error("Failed to close response body", "error", err)
		}
	}()

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

	if err := validateConfiguration(response.Configuration); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	response.Configuration.LoggingLevel = utils.LoggingLevel(params.LoggingLevel)
	response.Configuration.UseSyslog = params.UseSyslog
	response.Configuration.DisableAgentPostback = params.DisableAgentPostback
	response.Configuration.DisableAutoUpdates = params.NoAutoUpdates
	response.Configuration.GithubToken = params.GithubToken

	if params.MqttQos != -1 {
		qos := byte(params.MqttQos)
		response.Configuration.MqttQos = &qos
	}

	// Apply the optional tuning overrides. Each field is set only when the
	// operator provided the flag, otherwise it stays nil so the agent falls
	// back to its documented default.
	response.Configuration.MqttConnectTimeoutSeconds = tuningPtr(
		params.Tuning.MqttConnectTimeoutSeconds,
	)
	response.Configuration.MqttSubscribeTimeoutSeconds = tuningPtr(
		params.Tuning.MqttSubscribeTimeoutSeconds,
	)
	response.Configuration.WorkerCount = tuningPtr(params.Tuning.WorkerCount)
	response.Configuration.MessageQueueSize = tuningPtr(params.Tuning.MessageQueueSize)
	response.Configuration.PostbackMaxAttempts = tuningPtr(params.Tuning.PostbackMaxAttempts)
	response.Configuration.PostbackBaseRetryBackoffSeconds = tuningPtr(
		params.Tuning.PostbackBaseRetryBackoffSeconds,
	)
	response.Configuration.CommandTimeoutSeconds = tuningPtr(params.Tuning.CommandTimeoutSeconds)
	response.Configuration.SasTokenLifetimeHours = tuningPtr(params.Tuning.SasTokenLifetimeHours)
	response.Configuration.MaxOutputBytes = tuningPtr(params.Tuning.MaxOutputBytes)

	// Create the data directory. EnsureSecureDir (rather than a bare MkdirAll)
	// is what locks it to 0700 on Linux/macOS on both a fresh install and a
	// reinstall over an existing, previously world-readable installation
	// (sc-108849).
	dataDir := agent.GetDataDirectory(params.OrgId)
	err = params.FS.EnsureSecureDir(dataDir)
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

	// Written atomically so a failed write cannot leave a truncated config file
	// that the service is then unable to start from. SecureFileMode (0600
	// rather than the world-readable DefaultFileMod) is what keeps the Azure
	// IoT Hub SharedAccessKey and GitHub token it contains from being
	// plaintext-readable by any other local account (sc-108849). On Windows,
	// the file is not separately re-ACL'd here (unlike the service-start
	// migration in service.go): a freshly created file inherits the data
	// directory's ACL, which SecureDataDirectoryACL above already granted to
	// ServiceUsername when the service runs as an account other than the one
	// performing this install — an explicit per-file EnsureSecureFile call
	// would strip that inherited grant and lock the service out of its own
	// config file.
	err = writeFileAtomic(params.FS, configFilePath, configBytes, utils.SecureFileMode)
	if err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	name := agent.GetServiceName(params.OrgId)
	agentExecutablePath := agent.GetAgentExecutablePath(params.OrgId)

	// Stop and delete the service if it already exists
	existingService, err := params.ServiceManager.Open(name)
	if err == nil {
		stopped := false
		if existingService.IsActive() {
			logger.Info("Stopping service", "service", name)
			// Abort before deleting the registration or overwriting the
			// executable: a service that will not stop may still hold both.
			// Report the stop failure itself, not the handle cleanup, so the
			// reason the install aborted is visible.
			if stopErr := existingService.Stop(); stopErr != nil {
				if closeErr := existingService.Close(); closeErr != nil {
					logger.Error(
						"Failed to close service handle",
						"service", name,
						"error", closeErr,
					)
				}
				return fmt.Errorf("failed to stop service %s: %w", name, stopErr)
			}
			stopped = true
		}

		// Wait for the old process to actually exit before the registration is
		// deleted and the executable is replaced. A process that is still running
		// holds its own image open, so proceeding here is what fails the install
		// with a sharing violation on Windows.
		logger.Info(
			"Waiting for the agent process to exit",
			"service", name,
			"agent_executable", agentExecutablePath,
		)
		if waitErr := waitForAgentProcessExit(
			logger,
			existingService,
			params.FS,
			agentExecutablePath,
			params.exitWait,
		); waitErr != nil {
			// The config file was already refreshed above, so say so rather than
			// claiming nothing changed: the installed agent and its registration are
			// what had to be left alone while the old process is alive.
			logger.Error(
				"Install aborted; the existing agent and its service registration were left untouched",
				"service",
				name,
				"agent_executable",
				"not modified",
				"service_registration",
				"intact",
				"config_file",
				"updated",
				"error",
				waitErr,
			)
			// Put the endpoint back the way it was found rather than leaving a
			// stopped service behind.
			if stopped {
				if startErr := existingService.Start(); startErr != nil {
					logger.Error(
						"Failed to restart service after aborted install; the endpoint is offline",
						"service", name,
						"error", startErr,
					)
				}
			}
			if closeErr := existingService.Close(); closeErr != nil {
				logger.Error("Failed to close service handle", "service", name, "error", closeErr)
			}
			return fmt.Errorf("failed to wait for agent process to exit: %w", waitErr)
		}

		// Delete the service
		err = existingService.Delete()
		if err != nil {
			return fmt.Errorf("failed to delete service: %w", err)
		}
		logger.Info("Service deleted", "service", name)

		err = existingService.Close()
		if err != nil {
			return fmt.Errorf("failed to close service %s: %w", name, err)
		}

		// Wait for the deleted registration to be reaped so the name is free to
		// register again. A registration that outlives the deadline is reported and
		// creation is attempted anyway, which surfaces the real conflict.
		logger.Info("Waiting for the service registration to be removed", "service", name)
		if deregErr := waitForServiceDeregistration(
			params.ServiceManager,
			name,
			params.exitWait,
		); deregErr != nil {
			logger.Error(
				"Service registration outlived its deletion; registering anyway",
				"service", name,
				"error", deregErr,
			)
		}
	}

	// Windows has no POSIX mode bits for EnsureSecureDir to enforce, so the
	// data directory's ACL is locked down separately: SYSTEM, Administrators,
	// whichever account is running this installer, and the account the
	// service itself will run as when it differs (ServiceUsername) — so a
	// service configured to run as a different account than the installer is
	// not locked out of its own config file. No-op on non-Windows.
	//
	// Deliberately runs here rather than right after EnsureSecureDir above:
	// this resets ACLs recursively across every file in the directory (icacls
	// /T), and by this point any pre-existing agent process has been
	// confirmed exited (waitForAgentProcessExit above) — so nothing still
	// holds the directory's log file open. Doing this before that wait, while
	// a running agent still had its log open, is what corrupted read/write
	// access to it during integration testing; see the same reasoning on
	// service.go's Execute.
	//
	// Best effort rather than fatal: ServiceUsername may name an account that
	// does not exist or is not yet resolvable from this installer's context
	// (e.g. a domain account not yet visible), which icacls cannot grant.
	// Failing the whole install over that would be worse than the exposure
	// this is closing — the directory simply keeps its prior (pre-fix)
	// permissions for now; the next update re-attempts this same lockdown.
	if err := utils.SecureDataDirectoryACL(dataDir, params.ServiceUsername); err != nil {
		logger.Warn("Failed to secure data directory ACL", "path", dataDir, "error", err)
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

	// Written atomically so a failure here leaves any previously installed binary
	// byte-identical instead of truncated.
	err = writeFileAtomic(
		params.FS,
		agentExecutablePath,
		execFileBytes,
		utils.DefaultExecutableFileMod,
	)
	if err != nil {
		return fmt.Errorf("failed to create agent executable: %w", err)
	}

	logger.Info("Agent installed to", "path", agentExecutablePath)
	logger.Info(
		"Commands will be temporarily saved to",
		"path",
		agent.GetScriptsDirectory(params.OrgId),
	)

	// Create the service
	logger.Info("Creating service", "service", name)

	svc, err := params.ServiceManager.Create(service.AgentParams{
		Name:                name,
		AgentExecutablePath: agentExecutablePath,
		OrgId:               params.OrgId,
		ConfigFilePath:      configFilePath,
		LogFilePath:         agent.GetLogFilePath(params.OrgId),
		ServiceUsername:     params.ServiceUsername,
		ServicePassword:     params.ServicePassword,
	})
	if err != nil {
		return fmt.Errorf("failed to create service: %w", err)
	}
	defer func() {
		err := svc.Close()
		if err != nil {
			logger.Error("Failed to close service handle", "error", err)
		}
	}()
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
