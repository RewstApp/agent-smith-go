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
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/interpreter"
	"github.com/RewstApp/agent-smith-go/internal/mqtt"
	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
	"github.com/RewstApp/agent-smith-go/shared"

	"github.com/hashicorp/go-plugin"
)

type Status struct {
	Cpu      int  `json:"cpu"`
	Memory   int  `json:"memory"`
	Disk     int  `json:"disk"`
	Network  int  `json:"network"`
	IsOnline bool `json:"is_online"`
}

// handshakeConfigs are used to just do a basic handshake between
// a plugin and host. If the handshake fails, a user friendly error is shown.
// This prevents users from executing bad plugins or executing a plugin
// directory. It is a UX feature, not a security feature.
var handshakeConfig = plugin.HandshakeConfig{
	ProtocolVersion:  1,
	MagicCookieKey:   "BASIC_PLUGIN",
	MagicCookieValue: "hello",
}

// pluginMap is the map of plugins we can dispense.
var pluginMap = map[string]plugin.Plugin{
	"notifier": &shared.NotifierPlugin{},
}

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

	// START PLUGIN INIT
	execPath, err := os.Executable()
	if err != nil {
		log.Println("Failed to get os executable")
		return 1
	}

	pluginPath := filepath.Join(filepath.Dir(execPath), "plugins/agent-smith-httpd.exe")

	// We're a host! Start by launching the plugin process.
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig: handshakeConfig,
		Plugins:         pluginMap,
		Cmd:             exec.Command(pluginPath),
		Stderr:          log.Writer(),
	})
	defer client.Kill()

	// Connect via RPC
	rpcClient, err := client.Client()
	if err != nil {
		log.Println(err)
		return 1
	}

	// Request the plugin
	raw, err := rpcClient.Dispense("notifier")
	if err != nil {
		log.Println(err)
		return 1
	}

	// We should have a Greeter now! This feels like a normal interface
	// implementation but is in fact over an RPC connection.
	notifier := raw.(shared.Notifier)
	// END USE THE PLUGIN

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
	notifier.Notify("AgentStarted")
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
				notifier.Notify("AgentReconnecting")
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

				notifier.Notify("AgentReceivedMesage")

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
		notifier.Notify("AgentOnline")

		// Reset the timeout
		rg.Clear()
		rg.Next()

		// Wait for the stop/shutdown command or lost connection
		select {
		case <-stopped:
			notifier.Notify("AgentStopped")
			return 0
		case <-lost:
			notifier.Notify("AgentOffline")
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
