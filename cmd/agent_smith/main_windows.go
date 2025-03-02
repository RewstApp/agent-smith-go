//go:build windows

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/interpreter"
	"github.com/RewstApp/agent-smith-go/internal/mqtt"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func stateToString(state svc.State) string {
	switch state {
	case svc.Stopped:
		return "STOPPED"
	case svc.StartPending:
		return "PENDING"
	case svc.StopPending:
		return "STOP PENDING"
	case svc.Running:
		return "RUNNING"
	case svc.ContinuePending:
		return "CONTINUE PENDING"
	case svc.PausePending:
		return "PAUSE PENDING"
	case svc.Paused:
		return "PAUSED"
	default:
		return ""
	}
}

type service struct {
	OrgId      string
	ConfigFile string
	LogFile    string
}

func (service *service) Execute(args []string, request <-chan svc.ChangeRequest, response chan<- svc.Status) (bool, uint32) {
	response <- svc.Status{State: svc.StartPending}

	// Create context to cancel running commands
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Configure the logger
	logFile, err := os.OpenFile(service.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		response <- svc.Status{State: svc.Stopped}
		return false, 1
	}
	defer logFile.Close()
	utils.ConfigureLogger("agent_smith", logFile)

	// Show header
	log.Println("Agent Smith Version:", version.Version)
	log.Println("Running on:", runtime.GOOS)

	// Load the config file
	configFile, err := os.OpenFile(service.ConfigFile, os.O_RDONLY, 0644)
	if err != nil {
		log.Println("Failed to open config:", err)
		response <- svc.Status{State: svc.Stopped}
		return false, 1
	}
	defer configFile.Close()

	// Read and parse the config file
	configFileBytes, err := io.ReadAll(configFile)
	if err != nil {
		log.Println("Failed to read config:", err)
		response <- svc.Status{State: svc.Stopped}
		return false, 1
	}

	var device agent.Device

	err = json.Unmarshal(configFileBytes, &device)
	if err != nil {
		log.Println("Failed to parse config:", err)
		response <- svc.Status{State: svc.Stopped}
		return false, 1
	}

	// Create MQTT options
	opts, err := mqtt.NewClientOptions(device)
	if err != nil {
		log.Println("Failed to create client options:", err)
		response <- svc.Status{State: svc.Stopped}
		return false, 1
	}

	// Manually handle auto reconnection
	opts.SetAutoReconnect(false)

	// Add event handlers
	opts.OnConnectionLost = func(client mqtt.Client, err error) {
		log.Println("Connection lost:", err)
	}

	// Connect to the broker
	client := mqtt.NewClient(opts)
	token := client.Connect()

	if token.Wait() && token.Error() != nil {
		log.Println("Failed to connect:", token.Error())
		response <- svc.Status{State: svc.Stopped}
		return false, 1
	}
	defer client.Disconnect(250)

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

			err = message.Execute(ctx, &device)
			if err != nil {
				log.Println("Failed to execute message:", err)
				return
			}
		}()
	})

	if token.Wait() && token.Error() != nil {
		log.Println("Failed to subscribe:", err)
		response <- svc.Status{State: svc.Stopped}
		return false, 1
	}

	// Complete initialization
	log.Println("Subscribed to messages")
	response <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	// Wait for the stop or shutdown command
	for change := range request {
		switch change.Cmd {
		case svc.Stop, svc.Shutdown:
			response <- svc.Status{State: svc.Stopped}
			return true, 0
		}
	}

	// Request channel has been closed
	log.Println("Request channel closed")
	response <- svc.Status{State: svc.Stopped}
	return false, 1
}

func main() {
	// Parse command-line arguments
	var orgId string
	var configUrl string
	var configSecret string
	var configFile string
	var logFile string

	flag.StringVar(&orgId, "org-id", "", "Organization ID")

	// Config mode arguments
	flag.StringVar(&configUrl, "config-url", "", "Configuration URL")
	flag.StringVar(&configSecret, "config-secret", "", "Config secret")

	// Service mode arguments
	flag.StringVar(&configFile, "config-file", "", "Config file")
	flag.StringVar(&logFile, "log-file", "", "Log file")
	flag.Parse()

	// Make sure that org id is specified
	if orgId == "" {
		log.Println("Missing org-id parameter")
		return
	}

	// Run in config mode
	if configUrl != "" && configSecret != "" {
		// TODO: DO CONFIG URL
		return
	}

	// Run in service mode
	if configFile != "" && logFile != "" {
		// Check if this is running as a service
		isWinSvc, err := svc.IsWindowsService()
		if err != nil {
			log.Println("Failed to query execution status:", err)
			return
		}

		if !isWinSvc {
			log.Println("Executable should be run as a service")
			return
		}

		// Start the windows service
		err = svc.Run(agent.GetServiceName(orgId), &service{
			OrgId:      orgId,
			ConfigFile: configFile,
			LogFile:    logFile,
		})
		if err != nil {
			log.Println("Failed to run the service:", err)
		}

		log.Println("Service closed")
		return
	}

	// Run in service management mode
	tail := flag.Args()
	if len(tail) == 0 {
		log.Println("Missing command")
		return
	}

	// Connect to service manager
	svcMgr, err := mgr.Connect()
	if err != nil {
		log.Println("Failed to connect to service manager:", err)
		return
	}

	command := tail[0]
	name := agent.GetServiceName(orgId)

	if command == "status" {
		service, err := svcMgr.OpenService(name)
		if err != nil {
			log.Println("Failed to open service:", name)
			return
		}
		defer service.Close()

		status, err := service.Query()
		if err != nil {
			log.Println("Failed to query service status:", err)
			return
		}

		log.Println(stateToString(status.State))
		return
	}

	if command == "start" {
		service, err := svcMgr.OpenService(name)
		if err != nil {
			log.Println("Failed to open service:", name)
			return
		}
		defer service.Close()

		err = service.Start()
		if err != nil {
			log.Println("Failed to start service:", name)
			return
		}
		log.Println("Started")
		return
	}

	if command == "stop" {
		service, err := svcMgr.OpenService(name)
		if err != nil {
			log.Println("Failed to open service:", name)
			return
		}
		defer service.Close()

		_, err = service.Control(svc.Stop)
		if err != nil {
			log.Println("Failed to stop service:", name)
			return
		}
		log.Println("Stopped")
		return
	}

	log.Println("Unrecognized command:", command)
}
