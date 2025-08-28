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
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/interpreter"
	"github.com/RewstApp/agent-smith-go/internal/mqtt"
	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/syslog"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
	"github.com/RewstApp/agent-smith-go/plugins"
)

type errorResponse struct {
	Error string `json:"error"`
}

func (svc *serviceParams) loadConfig() (agent.Device, error) {
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

	return device, nil
}

func (svc *serviceParams) loadLog() (*os.File, error) {
	logFile, err := os.OpenFile(svc.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, utils.DefaultFileMod)
	if err != nil {
		return nil, err
	}

	return logFile, nil
}

func (svc *serviceParams) Name() string {
	return agent.GetServiceName(svc.OrgId)
}

func (svc *serviceParams) Execute(stop <-chan struct{}, running chan<- struct{}) service.ServiceExitCode {
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
	defer logFile.Close()

	logger := utils.ConfigureLogger("agent_smith", logFile, device.LoggingLevel)

	// Configure syslogger if needed
	if device.UseSyslog {
		sysLogger, err := syslog.New(svc.Name(), logFile)
		if err != nil {
			return service.LogFileError
		}
		defer sysLogger.Close()

		logger = utils.ConfigureLogger("agent_smith", sysLogger, device.LoggingLevel)
	}

	// Show header
	logger.Info("Agent Smith started", "version", version.Version, "os", runtime.GOOS, "device_id", device.DeviceId)

	defer func() {
		logger.Info("Service stopped")
	}()

	notifier, err := plugins.LoadNotifer(device.Plugins, logFile)
	if err != nil {
		logger.Warn("Failed to load plugin", "error", err)
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
	notifier.Notify("AgentStarted")
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
				notifier.Notify("AgentStatus:Reconnecting")
			}
		}

		// Move to the next timeout
		rg.Next()

		// Create a channel to wait for lost connection
		lost := make(chan struct{})

		// Create MQTT options
		opts, err := mqtt.NewClientOptions(device)
		if err != nil {
			logger.Error("Failed to create client options", "error", err)
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
			continue
		}
		defer client.Disconnect((uint)(mqtt.DefaultDisconnectQuiesce / time.Millisecond))

		// Subscribe to the topic
		topic := fmt.Sprintf("devices/%s/messages/devicebound/#", device.DeviceId)
		token = client.Subscribe(topic, 1, func(client mqtt.Client, msg mqtt.Message) {
			// Execute the payload on a goroutine so it won't block the receiver
			go func() {
				var message interpreter.Message
				err := message.Parse(msg.Payload())
				if err != nil {
					logger.Error("Parse failed", "error", err)
					return
				}

				notifier.Notify("AgentReceivedMessage:" + string(msg.Payload()))

				// Execute the message
				resultBytes := message.Execute(ctx, device, logger)

				// Skip if there is no post_id specified
				if message.PostId == "" {
					return
				}

				// Postback the response
				postbackReq, err := message.CreatePostbackRequest(ctx, device, bytes.NewReader(resultBytes))
				if err != nil {
					logger.Error("Failed to create postback request", "error", err)
					return
				}

				// Send the postback
				logger.Info("Sending postback", "post_id", message.PostId, "url", postbackReq.URL)
				client := &http.Client{}
				res, err := client.Do(postbackReq)
				if err != nil {
					logger.Error("Failed to send postback", "error", err)
					return
				}
				defer res.Body.Close()

				// Show postback response body if not empty
				bodyBytes, err := io.ReadAll(res.Body)
				if err != nil {
					logger.Error("Failed to read postback response body", "error", err)
					return
				}

				// Show success response
				if res.StatusCode == http.StatusOK {
					logger.Info("Postback sent", "post_id", message.PostId)
					if len(bodyBytes) > 0 {
						logger.Info("Received response", "data", string(bodyBytes))
					}
					return
				}

				// Process error
				var response errorResponse
				err = json.Unmarshal(bodyBytes, &response)

				// Error with different format
				if err != nil {
					logger.Error("Postback failed", "post_id", message.PostId, "status_code", res.StatusCode)
					if len(bodyBytes) > 0 {
						logger.Error("Received error response", "data", string(bodyBytes))
					}
					return
				}

				// Special error for webhook already fulfilled
				if res.StatusCode == http.StatusBadRequest && strings.Contains(strings.ToLower(response.Error), "fulfilled") {
					logger.Info("Postback already sent", "post_id", message.PostId)
					return
				}

				// Standard error format
				logger.Error("Postback failed", "post_id", message.PostId, "status_code", res.StatusCode, "message", response.Error)
			}()
		})

		if token.Wait() && token.Error() != nil {
			logger.Error("Failed to subscribe", "error", err)
			continue
		}

		// Complete initialization
		logger.Info("Subscribed to messages")
		notifier.Notify("AgentStatus:Online")

		// Reset the timeout
		rg.Clear()
		rg.Next()

		// Wait for the stop/shutdown command or lost connection
		select {
		case <-stopped:
			notifier.Notify("AgentStatus:Stopped")
			return 0
		case <-lost:
			notifier.Notify("AgentStatus:Offline")
			continue
		}
	}
}

func runService(params *serviceParams) {
	exitCode, _ := service.Run(params)
	os.Exit(exitCode)
}
