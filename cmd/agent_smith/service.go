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
	workerCount         = 10
	messageQueueSize    = 100
	postbackHTTPTimeout = 30 * time.Second
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
	defer notifier.Kill()

	if err != nil {
		logger.Warn("Failed to load plugin", "error", err)
	}

	plugins := notifier.Plugins()
	if len(plugins) == 1 {
		logger.Info("Plugin loaded", "plugin", plugins[0])
	} else if len(plugins) > 1 {
		logger.Info("Plugins loaded", "plugins", plugins)
	}

	// Create a channel for stopped signal
	stopped := make(chan struct{})

	// Monitor the request for the stopped signal
	go func() {
		for {
			select {
			case <-stop:
				stopped <- struct{}{}
			case <-ctx.Done():
				return
			}
		}
	}()

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

		// Create a fresh message queue and worker pool for this connection attempt.
		// Workers are closed and drained at every exit point so goroutines do not
		// accumulate across reconnection cycles.
		msgQueue := make(chan []byte, messageQueueSize)
		var wg sync.WaitGroup
		for i := range workerCount {
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
						svc.processMessage(payload, ctx, device, logger, notifier)
					case <-ctx.Done():
						logger.Debug("Message worker stopped: context cancelled", "worker", i)
						return
					}
				}
			}()
		}
		drainWorkers := func() {
			close(msgQueue)
			wg.Wait()
		}

		// Create a channel to wait for lost connection
		lost := make(chan struct{}, 1)

		// Create MQTT options
		opts, err := mqtt.NewClientOptions(device)
		if err != nil {
			logger.Error("Failed to create client options", "error", err)
			drainWorkers()
			return service.GenericError
		}

		// Manually handle auto reconnection
		opts.SetAutoReconnect(false)

		// Add event handlers
		opts.OnConnectionLost = func(client mqtt.Client, err error) {
			logger.Error("Connection lost", "error", err)
			lost <- struct{}{}
		}

		// Connect to the broker
		client := mqtt.NewClient(opts)
		token := client.Connect()

		if token.Wait() && token.Error() != nil {
			logger.Error("Failed to connect", "error", token.Error())
			drainWorkers()
			continue
		}
		disconnectQuiesce := (uint)(mqtt.DefaultDisconnectQuiesce / time.Millisecond)

		// Update device twin reported properties before subscribing
		err = mqtt.UpdateReportedProperties(client, mqtt.ReportedProperties{
			AgentVersion: version.Version,
		})
		if err != nil {
			logger.Warn("Failed to update device twin reported properties", "error", err)
		} else {
			logger.Info("Device twin reported properties updated", "agent_version", version.Version)
		}

		// Subscribe to the topic
		topic := fmt.Sprintf("devices/%s/messages/devicebound/#", device.DeviceId)
		qos := byte(1)
		if device.MqttQos != nil {
			qos = *device.MqttQos
		}
		token = client.Subscribe(topic, qos, func(client mqtt.Client, msg mqtt.Message) {
			payload := msg.Payload()
			select {
			case msgQueue <- payload:
			default:
				logger.Warn(
					"Message dropped: queue full",
					"queue_size",
					messageQueueSize,
				)
			}
		})

		if token.Wait() && token.Error() != nil {
			logger.Error("Failed to subscribe", "error", token.Error())
			client.Disconnect(disconnectQuiesce)
			drainWorkers()
			continue
		}

		// Complete initialization
		logger.Info("Subscribed to messages", "topic", topic, "qos", qos)
		_ = notifier.Notify("AgentStatus:Online") // Best effort notification

		// Reset the timeout
		rg.Clear()
		rg.Next()

		// Wait for the stop/shutdown command or lost connection
		select {
		case <-stopped:
			_ = notifier.Notify("AgentStatus:Stopped") // Best effort notification
			client.Disconnect(disconnectQuiesce)
			drainWorkers()
			return 0
		case <-lost:
			_ = notifier.Notify("AgentStatus:Offline") // Best effort notification
			client.Disconnect(disconnectQuiesce)
			drainWorkers()
			continue
		}
	}
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
		"AgentReceivedMessage:" + string(payload),
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

	// Postback the response
	postbackReq, err := message.CreatePostbackRequest(
		ctx,
		device,
		bytes.NewReader(resultBytes),
	)
	if err != nil {
		logger.Error("Failed to create postback request", "error", err)
		return
	}

	logger.Info("Sending postback", "post_id", message.PostId, "url", postbackReq.URL)
	res, err := svc.HTTPClient.Do(postbackReq)
	if err != nil {
		logger.Error("Failed to send postback", "error", err)
		return
	}
	defer func() {
		err = res.Body.Close()
		if err != nil {
			logger.Error("Failed to close response", "error", err)
		}
	}()

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		logger.Error("Failed to read postback response body", "error", err)
		return
	}

	if res.StatusCode == http.StatusOK {
		logger.Info("Postback sent", "post_id", message.PostId)
		if len(bodyBytes) > 0 {
			logger.Info("Received response", "data", string(bodyBytes))
		}
		return
	}

	var response errorResponse
	err = json.Unmarshal(bodyBytes, &response)
	if err != nil {
		logger.Error(
			"Postback failed",
			"post_id",
			message.PostId,
			"status_code",
			res.StatusCode,
		)
		if len(bodyBytes) > 0 {
			logger.Error("Received error response", "data", string(bodyBytes))
		}
		return
	}

	if res.StatusCode == http.StatusBadRequest &&
		strings.Contains(strings.ToLower(response.Error), "fulfilled") {
		logger.Info("Postback already sent", "post_id", message.PostId)
		return
	}

	logger.Error(
		"Postback failed",
		"post_id",
		message.PostId,
		"status_code",
		res.StatusCode,
		"message",
		response.Error,
	)
}

func runService(params *serviceContext) {
	exitCode, _ := service.Run(params)
	os.Exit(exitCode)
}
