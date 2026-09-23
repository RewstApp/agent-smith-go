package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
	"github.com/hashicorp/go-hclog"
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

	// Fetch, retrying a transient endpoint answer. The endpoint is a Rewst
	// workflow behind the engine's front door and answers a fraction of requests
	// with the engine's own ceiling (408), a gateway error (5xx) or a transient
	// routing 404; a fresh attempt a few seconds later succeeds. Before
	// sc-118306 the first such answer failed the install outright, and in the
	// integration suite it was the largest single source of red runs.
	bodyBytes, err := fetchConfigurationWithRetry(params, hostInfoBytes, logger)
	if err != nil {
		return err
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
	response.Configuration.LogMaxBytes = tuningPtr(params.Tuning.LogMaxBytes)
	response.Configuration.LogMaxFiles = tuningPtr(params.Tuning.LogMaxFiles)

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

// Config-fetch retry policy (sc-118306). Three attempts with a jittered
// exponential backoff starting at 5s and capped at 30s keep the worst case
// (~5 + ~10 + request time) well inside an installer's patience, while riding
// out the single slow minute at the endpoint that used to fail installs.
const (
	defaultConfigMaxAttempts      = 3
	defaultConfigBaseRetryBackoff = 5 * time.Second
	maxConfigRetryBackoff         = 30 * time.Second

	// transientConfig404Marker is the body the engine's front door returns when
	// it transiently cannot route to a trigger that exists (seen alongside 502s
	// on the same URL that answers 200 seconds later). A 404 without it is a
	// refusal: the trigger URL is wrong.
	transientConfig404Marker = "Workflow was not found"

	// configErrorBodyExcerpt bounds how much of a refusal body reaches the error.
	configErrorBodyExcerpt = 200
)

// configFetchError is one failed attempt at the config endpoint, classified so
// the caller knows whether another attempt can help.
type configFetchError struct {
	err       error
	transient bool
}

func (e *configFetchError) Error() string { return e.err.Error() }
func (e *configFetchError) Unwrap() error { return e.err }

// isTransientConfigStatus reports whether a non-2xx answer is the engine having
// a bad moment (retry) rather than refusing the request (do not retry).
func isTransientConfigStatus(status int, body []byte) bool {
	switch {
	case status == http.StatusRequestTimeout, status == http.StatusTooManyRequests:
		return true
	case status >= 500:
		return true
	case status == http.StatusNotFound:
		return bytes.Contains(body, []byte(transientConfig404Marker))
	}
	return false
}

// fetchConfigurationWithRetry runs fetchConfigurationOnce up to
// params.ConfigMaxAttempts times, sleeping a jittered, capped backoff between
// transient failures, and returns the first successful body. A refusal, or the
// last transient failure, is returned as-is with the attempt count appended so
// an install log says what happened.
func fetchConfigurationWithRetry(
	params *configContext,
	hostInfoBytes []byte,
	logger hclog.Logger,
) ([]byte, error) {
	attempts := params.ConfigMaxAttempts
	if attempts <= 0 {
		attempts = defaultConfigMaxAttempts
	}
	base := time.Duration(params.ConfigBaseRetryBackoffSeconds) * time.Second
	if base <= 0 {
		base = defaultConfigBaseRetryBackoff
	}
	sleep := params.retrySleep
	if sleep == nil {
		sleep = time.Sleep
	}

	for attempt := 1; ; attempt++ {
		body, err := fetchConfigurationOnce(params, hostInfoBytes, logger)
		if err == nil {
			return body, nil
		}
		var fe *configFetchError
		transient := errors.As(err, &fe) && fe.transient
		if !transient {
			return nil, err
		}
		if attempt >= attempts {
			if attempts > 1 {
				return nil, fmt.Errorf("%w (transient; gave up after %d attempts)", err, attempts)
			}
			return nil, err
		}
		delay := utils.JitteredBackoff(base, maxConfigRetryBackoff, attempt-1)
		logger.Info(
			"Config endpoint answered transiently; retrying",
			"attempt", attempt,
			"of", attempts,
			"error", err.Error(),
			"retry_in", delay,
		)
		sleep(delay)
	}
}

// fetchConfigurationOnce performs one POST to the config endpoint and returns
// the bounded response body on a 2xx. Failures are classified: a request that
// never completed and a transient status are retryable; any other non-2xx is a
// refusal and carries a body excerpt so the operator sees why.
func fetchConfigurationOnce(
	params *configContext,
	hostInfoBytes []byte,
	logger hclog.Logger,
) ([]byte, error) {
	req, err := utils.NewRequest("POST", params.ConfigUrl, bytes.NewReader(hostInfoBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("x-rewst-secret", params.ConfigSecret)
	req.Header.Set("Content-Type", "application/json")

	httpClient := params.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: configHTTPTimeout}
	}
	res, err := httpClient.Do(req)
	if err != nil {
		// Connection refused, DNS, or the configHTTPTimeout ceiling: nothing was
		// answered, and a blip during install is exactly the case worth a retry.
		return nil, &configFetchError{
			err:       fmt.Errorf("failed to execute http request: %w", err),
			transient: true,
		}
	}
	defer func() {
		if err := res.Body.Close(); err != nil {
			logger.Error("Failed to close response body", "error", err)
		}
	}()

	// Read through a bounded reader rather than a bare io.ReadAll: the endpoint
	// is trusted to be Rewst, but a compromised, misconfigured or hijacked one
	// could otherwise stream an unbounded body into memory for the whole
	// configHTTPTimeout window. One byte over the ceiling is enough to tell a
	// legitimate payload from an oversized one, and the fetch is aborted rather
	// than parsing whatever prefix arrived — a truncated body would either fail
	// to parse or, worse, parse into a partial configuration.
	bodyBytes, err := io.ReadAll(io.LimitReader(res.Body, maxConfigResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if res.StatusCode < 200 || res.StatusCode > 299 {
		if isTransientConfigStatus(res.StatusCode, bodyBytes) {
			return nil, &configFetchError{
				err:       fmt.Errorf("failed to fetch configuration: status %d", res.StatusCode),
				transient: true,
			}
		}
		excerpt := strings.TrimSpace(string(bodyBytes))
		if len(excerpt) > configErrorBodyExcerpt {
			excerpt = excerpt[:configErrorBodyExcerpt] + "…"
		}
		if excerpt != "" {
			return nil, fmt.Errorf(
				"failed to fetch configuration: status %d (refused; not retried): %s",
				res.StatusCode, excerpt,
			)
		}
		return nil, fmt.Errorf(
			"failed to fetch configuration: status %d (refused; not retried)", res.StatusCode,
		)
	}
	logger.Info("Successfully fetched configuration", "status_code", res.StatusCode)

	if int64(len(bodyBytes)) > maxConfigResponseSize {
		return nil, fmt.Errorf(
			"configuration response exceeds maximum allowed size of %d bytes",
			maxConfigResponseSize,
		)
	}
	return bodyBytes, nil
}
