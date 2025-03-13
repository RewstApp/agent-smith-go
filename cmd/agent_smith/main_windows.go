//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
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

type fetchConfigurationResponse struct {
	Configuration agent.Device `json:"configuration"`
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

	defer func() {
		log.Println("Service stopped")
		response <- svc.Status{State: svc.Stopped}
	}()

	// Read and parse the config file
	configFileBytes, err := os.ReadFile(service.ConfigFile)
	if err != nil {
		log.Println("Failed to read config:", err)
		return false, 1
	}

	var device agent.Device

	err = json.Unmarshal(configFileBytes, &device)
	if err != nil {
		log.Println("Failed to parse config:", err)
		return false, 1
	}

	// Create a channel for stopped signal
	stopped := make(chan struct{})

	// Monitor the request for the stopped signal
	go func() {
		for {
			select {
			case change := <-request:
				switch change.Cmd {
				case svc.Stop, svc.Shutdown:
					stopped <- struct{}{}
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	response <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	rg := utils.ReconnectTimeoutGenerator{}

	for {
		// Wait for the timeout
		if rg.Timeout() > 0 {
			log.Println("Reconnecting in", rg.Timeout())
			select {
			case <-stopped:
				return true, 0
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
			return false, 1
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

				if res.StatusCode != http.StatusBadRequest {
					if len(bodyBytes) > 0 {
						log.Println(string(bodyBytes))
					}
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
			return true, 0
		case <-lost:
			continue
		}
	}
}

func main() {
	// Parse command-line arguments
	var orgId string
	var configUrl string
	var configSecret string
	var configFile string
	var logFile string
	var uninstall bool

	flag.StringVar(&orgId, "org-id", "", "Organization ID")

	// Config mode arguments
	flag.StringVar(&configUrl, "config-url", "", "Configuration URL")
	flag.StringVar(&configSecret, "config-secret", "", "Config secret")

	// Service mode arguments
	flag.StringVar(&configFile, "config-file", "", "Config file")
	flag.StringVar(&logFile, "log-file", "", "Log file")

	// Service management arguments
	flag.BoolVar(&uninstall, "uninstall", false, "Uninstall the agent")

	flag.Parse()

	// Make sure that org id is specified
	if orgId == "" {
		log.Println("Missing org-id parameter")
		return
	}

	// Run uninstall routine
	if uninstall {
		runUninstall(orgId)
		return
	}

	// Run in config mode
	if configUrl != "" && configSecret != "" {
		// Get installation paths data
		var pathsData agent.PathsData
		err := pathsData.Load(context.Background(), orgId)
		if err != nil {
			log.Println("Failed to read paths:", err)
			return
		}

		// Fetch configuration
		hostInfoBytes, err := json.MarshalIndent(pathsData.Tags, "", "  ")
		if err != nil {
			log.Println("Failed to read host info:", err)
			return
		}

		// Prepare http request and send
		log.Println("Sending", string(hostInfoBytes), "to", configUrl)
		req, err := http.NewRequestWithContext(context.Background(), "POST", configUrl, bytes.NewReader(hostInfoBytes))
		if err != nil {
			log.Println("Failed to create request:", err)
			return
		}
		req.Header.Set("x-rewst-secret", configSecret)
		req.Header.Set("Content-Type", "application/json")

		client := &http.Client{}
		res, err := client.Do(req)
		if err != nil {
			log.Println(err)
			return
		}
		defer res.Body.Close()

		if res.StatusCode != http.StatusOK {
			log.Println("Failed to fetch configuration with status code:", res.StatusCode)
			return
		}

		bodyBytes, err := io.ReadAll(res.Body)
		if err != nil {
			log.Println("Failed to read response:", err)
			return
		}

		// Parse the fetch configuration response
		var response fetchConfigurationResponse
		err = json.Unmarshal(bodyBytes, &response)
		if err != nil {
			log.Println("Failed to parse response:", err)
			return
		}

		// Create the data directory
		dataDir := agent.GetDataDirectory(orgId)
		err = utils.CreateFolderIfMissing(dataDir)
		if err != nil {
			log.Println("Failed to create data directory:", err)
			return
		}

		// Save the configuration file
		configFilePath := agent.GetConfigFilePath(orgId)
		configBytes, err := json.MarshalIndent(response.Configuration, "", "  ")
		if err != nil {
			log.Println("Failed to print config file:", err)
			return
		}

		// Got configuration
		log.Println("Received configuration:", string(configBytes))

		err = os.WriteFile(configFilePath, configBytes, utils.DefaultFileMod)
		if err != nil {
			log.Println("Failed to save config:", err)
			return
		}

		log.Println("Configuration saved to", configFilePath)
		log.Println("Logs will be saved to", agent.GetLogFilePath(orgId))

		// Create the program directory
		programDir := agent.GetProgramDirectory(orgId)
		err = utils.CreateFolderIfMissing(programDir)
		if err != nil {
			log.Println("Failed to create program directory:", err)
			return
		}

		// Copy the agent executable
		execFilePath, err := os.Executable()
		if err != nil {
			log.Println("Failed to get executable:", err)
			return
		}

		execFileBytes, err := os.ReadFile(execFilePath)
		if err != nil {
			log.Println("Failed to read executable file:", err)
			return
		}

		agentExecutablePath := agent.GetAgentExecutablePath(orgId)
		err = os.WriteFile(agentExecutablePath, execFileBytes, utils.DefaultFileMod)
		if err != nil {
			log.Println("Failed to create agent executable:", err)
			return
		}

		log.Println("Agent installed to", agentExecutablePath)
		log.Println("Commands will be temporarily saved to", agent.GetScriptsDirectory(orgId))

		// Create the service
		svcMgr, err := mgr.Connect()
		if err != nil {
			log.Println("Failed to connect service manager:", err)
			return
		}
		defer svcMgr.Disconnect()

		name := agent.GetServiceName(orgId)
		log.Println("Creating service", name, "...")

		svc, err := svcMgr.CreateService(name, agentExecutablePath, mgr.Config{
			StartType:        mgr.StartAutomatic,
			Description:      fmt.Sprintf("Rewst Remote Agent for Org %s", orgId),
			DelayedAutoStart: true,
		}, "--org-id", orgId, "--config-file", configFilePath, "--log-file", agent.GetLogFilePath(orgId))
		if err != nil {
			log.Println("Failed to create service:", err)
			return
		}
		defer svc.Close()
		log.Println("Service created")

		// Start the service
		log.Println("Starting service", name, "...")
		err = svc.Start()
		if err != nil {
			log.Println("Failed to start service:", err)
			return
		}

		log.Println("Service started")
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
			return
		}

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
