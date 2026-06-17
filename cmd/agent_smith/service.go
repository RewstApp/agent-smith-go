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
	"strings"
	"sync"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/interpreter"
	"github.com/RewstApp/agent-smith-go/internal/mqtt"
	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/syslog"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
	"github.com/RewstApp/agent-smith-go/plugins"
	"github.com/hashicorp/go-hclog"
)

const (
	// workerCount and messageQueueSize are the defaults applied when the device
	// config does not override them via worker_count / message_queue_size. The
	// effective values are resolved per cycle from the device config; see
	// agent.Device.ResolvedWorkerCount / ResolvedMessageQueueSize.
	workerCount              = agent.DefaultWorkerCount
	messageQueueSize         = agent.DefaultMessageQueueSize
	postbackHTTPTimeout      = 30 * time.Second
	postbackMaxAttempts      = 3
	postbackBaseRetryBackoff = 1 * time.Second

	// maxNotificationPayloadBytes bounds how many bytes of a received message
	// payload are embedded in the AgentReceivedMessage notification forwarded to
	// plugins. Payloads at or below this size are sent verbatim (preserving the
	// existing behaviour for normal-sized messages); larger payloads are
	// summarised so a single oversized workflow message cannot inflate agent
	// memory or overflow the plugin RPC pipe.
	maxNotificationPayloadBytes = 4096
)

type errorResponse struct {
	Error string `json:"error"`
}

func (svc *serviceContext) loadConfig() (agent.Device, error) {
	var device agent.Device

	// Read and parse the config file
	configFileBytes, err := os.ReadFile(svc.ConfigFile)
	if err != nil {
		return device, err
	}

	// Decode the config file
	err = json.Unmarshal(configFileBytes, &device)
	if err != nil {
		return device, err
	}

	if device.MqttQos != nil && *device.MqttQos > 2 {
		return device, fmt.Errorf("mqtt_qos must be 0, 1, or 2; got %d", *device.MqttQos)
	}

	return device, nil
}

func (svc *serviceContext) loadLog() (*os.File, error) {
	logFile, err := os.OpenFile(
		svc.LogFile,
		os.O_APPEND|os.O_CREATE|os.O_WRONLY,
		utils.DefaultFileMod,
	)
	if err != nil {
		return nil, err
	}

	return logFile, nil
}

func (svc *serviceContext) Name() string {
	return agent.GetServiceName(svc.OrgId)
}

func (svc *serviceContext) Execute(
	stop <-chan struct{},
	running chan<- struct{},
) service.ServiceExitCode {
	// Create context to cancel running commands
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load config
	device, err := svc.loadConfig()
	if err != nil {
		return service.ConfigError
	}

	// Configure the logger
	logFile, err := svc.loadLog()
	if err != nil {
		return service.LogFileError
	}
	defer func() {
		_ = logFile.Close()
	}()

	logger := utils.ConfigureLogger("agent_smith", logFile, device.LoggingLevel)

	// Configure syslogger if needed
	if device.UseSyslog {
		sysLogger, err := syslog.New(svc.Name(), logFile)
		if err != nil {
			return service.LogFileError
		}
		defer func() {
			err = sysLogger.Close()
			if err != nil {
				logger.Error("Failed to close sys logger handle", "error", err)
			}
		}()

		logger = utils.ConfigureLogger("agent_smith", sysLogger, device.LoggingLevel)
	}

	if !device.DisableAutoUpdates {
		updater := agent.NewUpdater(
			logger,
			&device,
			"https://api.github.com/repos/rewstapp/agent-smith-go/releases/latest",
			device.GithubToken,
			func(path string, args []string) error {
				return detachedCommand(path, args, logFile, logFile).Start()
			},
		)
		runner := agent.NewAutoUpdateRunner(
			logger,
			updater,
			agent.DefaultUpdateInterval(),
			agent.DefaultMaxRetries(),
			agent.DefaultBaseBackoff(),
		)
		runner.Start()
		defer runner.Stop()
	}

	// Show header
	logger.Info(
		"Agent Smith started",
		"version",
		version.Version,
		"os",
		runtime.GOOS,
		"device_id",
		device.DeviceId,
		"logging_level",
		device.LoggingLevel,
	)

	defer func() {
		logger.Info("Service stopped")
	}()

	notifier, err := plugins.LoadNotifer(device.Plugins, logFile)
	if err != nil {
		logger.Warn("Failed to load plugin", "error", err)
	}
	defer notifier.Kill()

	plugins := notifier.Plugins()
	if len(plugins) == 1 {
		logger.Info("Plugin loaded", "plugin", plugins[0])
	} else if len(plugins) > 1 {
		logger.Info("Plugins loaded", "plugins", plugins)
	}

	// Create a channel for stopped signal. It is closed (never sent to) when a
	// stop is requested so that closing can never block the monitor goroutine.
	// A closed channel makes every select on <-stopped return immediately and
	// permanently, decoupling teardown timing from the reconnect backoff
	// schedule: a stop arriving while no runCycle is draining stopped (e.g.
	// during the time.After reconnect wait) is still honored at once.
	stopped := make(chan struct{})

	// Monitor the request for the stopped signal
	utils.SafeGo(logger, func() {
		select {
		case <-stop:
			close(stopped)
		case <-ctx.Done():
		}
	}, "scope", "stop_monitor")

	running <- struct{}{}
	_ = notifier.Notify("AgentStarted") // Best effort notification
	rg := utils.ReconnectTimeoutGenerator{}

	for {
		// Wait for the timeout
		if rg.Timeout() > 0 {
			logger.Info("Reconnecting in", "timeout", rg.Timeout())
			select {
			case <-stopped:
				return 0
			case <-time.After(rg.Timeout()):
				logger.Info("Reconnecting...")
				_ = notifier.Notify("AgentStatus:Reconnecting") // Best effort notification
			}
		}

		// Move to the next timeout
		rg.Next()

		shouldReturn, clearBackoff, exitCode := svc.runCycle(ctx, device, logger, notifier, stopped)
		if clearBackoff {
			rg.Clear()
			rg.Next()
		}
		if shouldReturn {
			return exitCode
		}
	}
}

// runCycle runs one MQTT connection attempt through to disconnect. It returns
// (shouldReturn, clearBackoff, exitCode): shouldReturn signals Execute to exit
// with exitCode; clearBackoff signals that a successful connection was
// established and the reconnect backoff should be reset.
//
// A fresh cycleCtx is derived from the parent ctx for each invocation so
// in-flight commands (run via exec.CommandContext) are cancelled when the
// cycle ends. Commands started in a later cycle bind to that cycle's own
// context and are unaffected by the previous cycle's cancellation.
//
// Cleanup is guaranteed on all exit paths. The deferred teardown runs in LIFO
// order: MQTT teardown (Unsubscribe → Disconnect) first so no new messages
// arrive, then cycleCancel to interrupt any hung commands, then close the
// queue and wait for workers. Cancelling before wg.Wait is required —
// otherwise a hung command would block the wait indefinitely. Consolidating
// Unsubscribe and Disconnect in one defer ensures persistent (non-clean)
// Azure IoT Hub sessions don't retain server-side subscriptions across
// reconnects, which would re-deliver buffered messages and cause duplicate
// command execution.
func (svc *serviceContext) runCycle(
	ctx context.Context,
	device agent.Device,
	logger hclog.Logger,
	notifier plugins.NotifierWrapper,
	stopped <-chan struct{},
) (bool, bool, service.ServiceExitCode) {
	cycleCtx, cycleCancel := context.WithCancel(ctx)

	resolvedWorkerCount := device.ResolvedWorkerCount()
	resolvedQueueSize := device.ResolvedMessageQueueSize()

	msgQueue := make(chan []byte, resolvedQueueSize)

	// draining is closed at the very start of teardown (its defer is registered
	// last, so it runs first) to release the subscribe callback if it is blocked
	// applying back-pressure on a full queue. Releasing the callback before the
	// MQTT Unsubscribe/Disconnect is what keeps teardown deadlock-free: paho
	// dispatches messages on a single ordered goroutine, so a permanently
	// blocked callback would also stall the UNSUBACK/disconnect handling.
	draining := make(chan struct{})

	var wg sync.WaitGroup
	for i := range resolvedWorkerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Debug("Message worker started", "worker", i)
			for {
				select {
				case payload, ok := <-msgQueue:
					if !ok {
						logger.Debug("Message worker stopped: queue closed", "worker", i)
						return
					}
					logger.Debug(
						"Message worker processing",
						"worker", i,
						"queue_length", len(msgQueue),
					)
					svc.processMessageGuarded(i, payload, cycleCtx, device, logger, notifier)
				case <-cycleCtx.Done():
					logger.Debug("Message worker stopped: context cancelled", "worker", i)
					return
				}
			}
		}()
	}
	defer func() {
		cycleCancel()
		close(msgQueue)
		wg.Wait()
	}()

	// Create a channel to wait for lost connection
	lost := make(chan struct{}, 1)

	opts, err := mqtt.NewClientOptions(device)
	if err != nil {
		logger.Error("Failed to create client options", "error", err)
		return true, false, service.GenericError
	}

	opts.SetAutoReconnect(false)
	opts.OnConnectionLost = func(client mqtt.Client, err error) {
		logger.Error("Connection lost", "error", err)
		lost <- struct{}{}
	}

	topic := fmt.Sprintf("devices/%s/messages/devicebound/#", device.DeviceId)
	qos := byte(1)
	if device.MqttQos != nil {
		qos = *device.MqttQos
	}

	disconnectQuiesce := (uint)(mqtt.DefaultDisconnectQuiesce / time.Millisecond)
	client := mqtt.NewClient(opts)
	subscribed := false
	defer func() {
		if subscribed && client.IsConnected() {
			if token := client.Unsubscribe(topic); token.Wait() && token.Error() != nil {
				logger.Warn("Failed to unsubscribe", "topic", topic, "error", token.Error())
			}
		}
		client.Disconnect(disconnectQuiesce)
	}()

	token := client.Connect()
	if token.Wait() && token.Error() != nil {
		logger.Error("Failed to connect", "error", token.Error())
		return false, false, 0
	}

	err = mqtt.UpdateReportedProperties(client, mqtt.ReportedProperties{
		AgentVersion: version.Version,
	})
	if err != nil {
		logger.Warn("Failed to update device twin reported properties", "error", err)
	} else {
		logger.Info("Device twin reported properties updated", "agent_version", version.Version)
	}

	// Closed first during teardown (registered after the MQTT teardown defer so
	// it runs before it under LIFO) to unblock a back-pressured callback.
	defer close(draining)

	// enqueueMessage applies back-pressure instead of dropping; see its doc for
	// the delivery guarantee and the single (loudly surfaced) teardown drop path.
	token = client.Subscribe(topic, qos, func(client mqtt.Client, msg mqtt.Message) {
		svc.enqueueMessage(msg.Payload(), msgQueue, draining, resolvedQueueSize, logger, notifier)
	})

	if token.Wait() && token.Error() != nil {
		logger.Error("Failed to subscribe", "error", token.Error())
		return false, false, 0
	}
	subscribed = true

	logger.Info("Subscribed to messages", "topic", topic, "qos", qos)
	_ = notifier.Notify("AgentStatus:Online") // Best effort notification

	select {
	case <-stopped:
		_ = notifier.Notify("AgentStatus:Stopped") // Best effort notification
		return true, true, 0
	case <-lost:
		_ = notifier.Notify("AgentStatus:Offline") // Best effort notification
		return false, true, 0
	}
}

// buildReceivedMessageNotification builds the bounded "AgentReceivedMessage"
// notification string forwarded to plugins for a received payload. Payloads at
// or below maxNotificationPayloadBytes are embedded verbatim. Larger payloads
// are summarised as their total byte length plus a truncated prefix, so the
// resulting notification stays a fixed maximum size regardless of payload size —
// avoiding extra full-payload copies and the risk of overflowing the plugin RPC
// pipe.
func buildReceivedMessageNotification(payload []byte) string {
	const prefix = "AgentReceivedMessage:"

	if len(payload) <= maxNotificationPayloadBytes {
		return prefix + string(payload)
	}

	return fmt.Sprintf(
		"%s[truncated %d bytes] %s",
		prefix,
		len(payload),
		payload[:maxNotificationPayloadBytes],
	)
}

// enqueueMessage hands a received payload to the worker queue, applying
// back-pressure rather than dropping. paho dispatches messages on a single
// ordered goroutine and sends the QoS-1 PUBACK only after the subscribe callback
// (and thus this function) returns, so blocking here stops the broker from
// considering the message delivered: when the queue is full the call waits until
// a worker frees a slot, and if the agent stays saturated paho's inbound buffer
// fills so the broker holds and later redelivers messages instead of the agent
// silently discarding them.
//
// The single drop path is a payload arriving while the cycle is tearing down
// (draining closed). The connection is going away regardless, so at QoS >= 1 the
// broker redelivers on the next connection; the drop is therefore surfaced
// loudly — an Error log, a cumulative counter, and a best-effort plugin
// notification — rather than buried in a single Warn. draining is selected as a
// bounded escape so teardown can never deadlock on a full queue.
//
// Returns true when the payload was enqueued, false when it was dropped.
func (svc *serviceContext) enqueueMessage(
	payload []byte,
	msgQueue chan<- []byte,
	draining <-chan struct{},
	queueSize int,
	logger hclog.Logger,
	notifier plugins.NotifierWrapper,
) bool {
	select {
	case msgQueue <- payload:
		return true
	case <-draining:
		dropped := svc.droppedMessages.Add(1)
		logger.Error(
			"Message dropped: received during shutdown, broker will redeliver at QoS>=1",
			"queue_size", queueSize,
			"dropped_total", dropped,
		)
		_ = notifier.Notify(
			fmt.Sprintf("AgentMessageDropped:shutdown (dropped_total=%d)", dropped),
		) // Best effort notification
		return false
	}
}

// processMessageGuarded runs processMessage with per-message panic recovery.
// Because the payload is untrusted (received over MQTT), a malformed message,
// an unexpected nil, a plugin RPC fault, or any library panic on this path must
// not crash the process. Recovering per-message — rather than per-worker-loop —
// contains the fault to the single offending message and keeps the worker alive
// to process the next item, so the pool stays at full strength. The recovered
// value and a stack trace are logged at Error level (with the worker id) to aid
// diagnosis. Normal error returns from processMessage are unaffected.
func (svc *serviceContext) processMessageGuarded(
	workerId int,
	payload []byte,
	ctx context.Context,
	device agent.Device,
	logger hclog.Logger,
	notifier plugins.NotifierWrapper,
) {
	defer utils.Recover(logger, "worker", workerId, "scope", "processMessage")
	svc.processMessage(payload, ctx, device, logger, notifier)
}

func (svc *serviceContext) processMessage(
	payload []byte,
	ctx context.Context,
	device agent.Device,
	logger hclog.Logger,
	notifier plugins.NotifierWrapper,
) {
	var message interpreter.Message
	err := message.Parse(payload)
	if err != nil {
		logger.Error("Parse failed", "error", err)
		return
	}

	_ = notifier.Notify(
		buildReceivedMessageNotification(payload),
	) // Best effort notification

	// Execute the message
	resultBytes := message.Execute(
		svc.Executor,
		ctx,
		device,
		logger,
		svc.Sys,
		svc.Domain,
	)

	// Skip if there is no post_id specified
	if message.PostId == "" {
		return
	}

	// Skip postback if disabled in config (ignored when executor always posts back)
	if device.DisableAgentPostback && !svc.Executor.AlwaysPostback() {
		return
	}

	svc.sendPostbackWithRetry(ctx, &message, device, resultBytes, logger)
}

// sendPostbackWithRetry posts the command result to the Rewst engine, retrying
// transient failures (network errors and 5xx responses) with exponential
// backoff. Non-retryable responses (2xx success, 400 "already fulfilled", and
// other 4xx errors) terminate the loop immediately. When all attempts fail the
// final failure is surfaced clearly so the result is not silently dropped.
func (svc *serviceContext) sendPostbackWithRetry(
	ctx context.Context,
	message *interpreter.Message,
	device agent.Device,
	resultBytes []byte,
	logger hclog.Logger,
) {
	maxAttempts := svc.PostbackMaxAttempts
	if maxAttempts < 1 {
		maxAttempts = postbackMaxAttempts
	}
	baseBackoff := svc.PostbackBaseRetryBackoff
	if baseBackoff <= 0 {
		baseBackoff = postbackBaseRetryBackoff
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			backoff := baseBackoff * (1 << (attempt - 2))
			logger.Info(
				"Retrying postback",
				"post_id", message.PostId,
				"attempt", attempt,
				"max_attempts", maxAttempts,
				"backoff", backoff,
			)
			select {
			case <-ctx.Done():
				logger.Error(
					"Postback aborted before retry: context cancelled",
					"post_id", message.PostId,
					"attempts", attempt-1,
					"error", ctx.Err(),
				)
				return
			case <-time.After(backoff):
			}
		}

		done, err := svc.attemptPostback(ctx, message, device, resultBytes, logger, attempt)
		if done {
			return
		}
		lastErr = err
	}

	logger.Error(
		"Postback failed: all retries exhausted, result dropped",
		"post_id", message.PostId,
		"attempts", maxAttempts,
		"last_error", lastErr,
	)
}

// attemptPostback performs a single postback attempt. It returns done=true
// when no further retries should occur (success, "already fulfilled", or a
// non-retryable 4xx response). When done=false the caller should retry; the
// returned error describes the most recent failure for the final summary log.
func (svc *serviceContext) attemptPostback(
	ctx context.Context,
	message *interpreter.Message,
	device agent.Device,
	resultBytes []byte,
	logger hclog.Logger,
	attempt int,
) (bool, error) {
	postbackReq, err := message.CreatePostbackRequest(
		ctx,
		device,
		bytes.NewReader(resultBytes),
	)
	if err != nil {
		logger.Error(
			"Failed to create postback request",
			"post_id", message.PostId,
			"attempt", attempt,
			"error", err,
		)
		return true, err
	}

	if attempt == 1 {
		logger.Info("Sending postback", "post_id", message.PostId, "url", postbackReq.URL)
	}

	res, err := svc.HTTPClient.Do(postbackReq)
	if err != nil {
		logger.Error(
			"Failed to send postback",
			"post_id", message.PostId,
			"attempt", attempt,
			"error", err,
		)
		return false, err
	}
	defer func() {
		if cerr := res.Body.Close(); cerr != nil {
			logger.Error("Failed to close response", "error", cerr)
		}
	}()

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		logger.Error(
			"Failed to read postback response body",
			"post_id", message.PostId,
			"attempt", attempt,
			"error", err,
		)
		return false, err
	}

	if res.StatusCode == http.StatusOK {
		logger.Info("Postback sent", "post_id", message.PostId, "attempt", attempt)
		if len(bodyBytes) > 0 {
			logger.Info("Received response", "data", string(bodyBytes))
		}
		return true, nil
	}

	var response errorResponse
	parseErr := json.Unmarshal(bodyBytes, &response)

	if parseErr == nil && res.StatusCode == http.StatusBadRequest &&
		strings.Contains(strings.ToLower(response.Error), "fulfilled") {
		logger.Info("Postback already sent", "post_id", message.PostId)
		return true, nil
	}

	// 5xx responses (and any other unexpected non-2xx without a parseable body)
	// are treated as transient. 4xx responses with a parseable error body are
	// terminal — retrying a malformed request will not help.
	retryable := res.StatusCode >= 500 || parseErr != nil

	if retryable {
		logger.Error(
			"Postback failed (will retry if attempts remain)",
			"post_id", message.PostId,
			"attempt", attempt,
			"status_code", res.StatusCode,
			"message", response.Error,
		)
		if parseErr != nil && len(bodyBytes) > 0 {
			logger.Error("Received error response", "data", string(bodyBytes))
		}
		return false, fmt.Errorf("postback failed: status %d: %s", res.StatusCode, response.Error)
	}

	logger.Error(
		"Postback failed (non-retryable)",
		"post_id", message.PostId,
		"attempt", attempt,
		"status_code", res.StatusCode,
		"message", response.Error,
	)
	return true, fmt.Errorf("postback failed: status %d: %s", res.StatusCode, response.Error)
}

func runService(params *serviceContext) {
	exitCode, _ := service.Run(params)
	os.Exit(exitCode)
}
