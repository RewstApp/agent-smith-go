package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/interpreter"
	"github.com/RewstApp/agent-smith-go/internal/mqtt"
	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
)

func (service *serviceParams) Name() string {
	return agent.GetServiceName(service.OrgId)
}

func (service *serviceParams) Execute(stop <-chan struct{}, running chan<- struct{}) int {
	// Create context to cancel running commands
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Configure the logger
	logFile, err := os.OpenFile(service.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return 1
	}
	defer logFile.Close()
	utils.ConfigureLogger("agent_smith", logFile)

	// Show header
	log.Println("Agent Smith Version:", version.Version)
	log.Println("Running on:", runtime.GOOS)

	defer func() {
		log.Println("Service stopped")
	}()

	// Read and parse the config file
	configFileBytes, err := os.ReadFile(service.ConfigFile)
	if err != nil {
		log.Println("Failed to read config:", err)
		return 1
	}

	var device agent.Device

	err = json.Unmarshal(configFileBytes, &device)
	if err != nil {
		log.Println("Failed to parse config:", err)
		return 1
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
	rg := utils.ReconnectTimeoutGenerator{}

	for {
		// Wait for the timeout
		if rg.Timeout() > 0 {
			log.Println("Reconnecting in", rg.Timeout())
			select {
			case <-stopped:
				return 0
			case <-time.After(rg.Timeout()):
				log.Println("Reconnecting...")
			}
		}

		// Move to the next timeout
		rg.Next()

		// Create a channel to wait for lost connection
		lost := make(chan struct{})

		// Create MQTT options
		opts, err := mqtt.NewClientOptions(device)
		if err != nil {
			log.Println("Failed to create client options:", err)
			return 1
		}

		// Manually handle auto reconnection
		opts.SetAutoReconnect(false)

		// Add event handlers
		opts.OnConnectionLost = func(client mqtt.Client, err error) {
			log.Println("Connection lost:", err)
			lost <- struct{}{}
		}

		// Connect to the broker
		client := mqtt.NewClient(opts)
		token := client.Connect()

		if token.Wait() && token.Error() != nil {
			log.Println("Failed to connect:", token.Error())
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
					log.Println("Parse failed:", err)
					return
				}

				// Execute the message
				resultBytes := message.Execute(ctx, device)

				// Postback the response
				postbackReq, err := message.CreatePostbackRequest(ctx, device, bytes.NewReader(resultBytes))
				if err != nil {
					log.Println("Failed to create postback request:", err)
					return
				}

				// Send the postback
				log.Println("Sending Postback", message.PostId, "to", postbackReq.URL, "...")
				client := &http.Client{}
				res, err := client.Do(postbackReq)
				if err != nil {
					log.Println("Failed to send postback:", err)
					return
				}
				defer res.Body.Close()

				if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusBadRequest {
					log.Println("Postback", message.PostId, "failed with status code:", res.StatusCode)
				} else {
					log.Println("Postback", message.PostId, "sent")
				}

				// Show postback response body if not empty
				bodyBytes, err := io.ReadAll(res.Body)
				if err != nil {
					log.Println("Failed to read postback response body:", err)
					return
				}

				if len(bodyBytes) > 0 && res.StatusCode != http.StatusBadRequest {
					log.Println(string(bodyBytes))
				}
			}()
		})

		if token.Wait() && token.Error() != nil {
			log.Println("Failed to subscribe:", err)
			continue
		}

		// Complete initialization
		log.Println("Subscribed to messages")

		// Reset the timeout
		rg.Clear()
		rg.Next()

		// Wait for the stop/shutdown command or lost connection
		select {
		case <-stopped:
			return 0
		case <-lost:
			continue
		}
	}
}

func runService(params *serviceParams) {
	exitCode, err := service.Run(params)
	if err != nil {
		log.Println("Failed to run service:", err)
	}

	os.Exit(exitCode)
}
