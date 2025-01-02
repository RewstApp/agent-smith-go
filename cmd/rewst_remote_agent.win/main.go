package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/RewstApp/agent-smith-go/pkg/version"

	"golang.org/x/text/encoding/unicode"

	"golang.org/x/text/transform"

	"golang.org/x/sys/windows/svc"

	"github.com/amenzhinsky/iothub/iotdevice"
	iotmqtt "github.com/amenzhinsky/iothub/iotdevice/transport/mqtt"
)

type ExecuteMessage struct {
	PostId              string  `json:"post_id"`
	Commands            string  `json:"commands"`
	InterpreterOverride *string `json:"interpreter_override"`
}

func (m ExecuteMessage) GetCommands() (string, error) {
	content, err := base64.StdEncoding.DecodeString(m.Commands)
	if err != nil {
		return "", nil
	}

	return string(content), nil
}

type AgentService struct {
	Configuration Config
}

// Execute is the main entry point for your service logic
func (m *AgentService) Execute(args []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {

	// Notify Windows that the service is starting
	status <- svc.Status{State: svc.StartPending}

	connStr := m.Configuration.ConnectionString()
	log.Println("Connecting to Iot Hub:", connStr)

	// Create a new device client
	client, err := iotdevice.NewFromConnectionString(iotmqtt.New(), m.Configuration.ConnectionString())
	if err != nil {
		log.Println("Failed to create client:", err)
		status <- svc.Status{State: svc.Stopped}
		return true, 1
	}
	log.Println("Client created")

	// Connect to IoT Hub
	if err = client.Connect(context.Background()); err != nil {
		log.Println("Failed to connect to Iot Hub:", err)
		status <- svc.Status{State: svc.Stopped}
		return true, 1
	}
	log.Println("Connected to Iot Hub")

	// Indicate the service is running
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	log.Println("Service is running...")

	// Subscribe to events
	sub, err := client.SubscribeEvents(context.Background())
	if err != nil {
		log.Println("Failed to subscribe events:", err)
		status <- svc.Status{State: svc.Stopped}
		return true, 1
	}
	log.Println("Subscribed to events")

	// Main service loop
	for {
		select {
		case r := <-req:
			switch r.Cmd {
			case svc.Stop, svc.Shutdown:
				log.Println("Service is stopping...")
				status <- svc.Status{State: svc.StopPending}

				if err = client.Close(); err != nil {
					log.Println("Closing client failed:", err)
					return true, 1
				}

				log.Println("Client closed")

				status <- svc.Status{State: svc.Stopped}

				return true, 0
			}
		case msg := <-sub.C():
			if err = Execute(msg.Payload); err != nil {
				log.Println("Failed to execute message:", err)
			}
		}
	}
}

type Config struct {
	DeviceId        string `json:"device_id"`
	RewstOrgId      string `json:"rewst_org_id"`
	RewstEngineHost string `json:"rewst_engine_host"`
	SharedAccessKey string `json:"shared_access_key"`
	AzureIotHubHost string `json:"azure_iot_hub_host"`
}

func (c Config) ConnectionString() string {
	return "HostName=" + c.AzureIotHubHost + ";DeviceId=" + c.DeviceId + ";SharedAccessKey=" + c.SharedAccessKey
}

func baseDirectory() (string, error) {
	// Get the path of the current executable
	exePath, err := os.Executable()
	if err != nil {
		return "", err
	}

	// Get the directory from the executable path
	return filepath.Dir(exePath), nil
}

func load(configFilePath string, out *Config) error {

	// Open the JSON file
	file, err := os.Open(configFilePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Read the file contents
	data, err := io.ReadAll(file)
	if err != nil {
		return err
	}

	// Parse the JSON data
	err = json.Unmarshal(data, out)
	if err != nil {
		return err
	}

	// No error
	return nil
}

func Execute(data []byte) error {
	var message ExecuteMessage
	err := json.Unmarshal(data, &message)
	if err != nil {
		return err
	}

	// Print contents of message
	log.Println("Received message:")
	log.Println("post_id", message.PostId)
	log.Println("commands", message.Commands)
	log.Println("interpreter_override", message.InterpreterOverride)

	// Parse the commands
	commandBytes, err := message.GetCommands()
	if err != nil {
		return err
	}

	// TODO: Add support for other interpreters
	// Decode using UTF16LE
	decoder := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder()
	commands, _, err := transform.String(decoder, string(commandBytes))
	if err != nil {
		return err
	}
	log.Println("parsed commands:", commands)

	// Run the command in the system using powershell
	cmd := exec.Command("powershell", "-Command", commands)
	err = cmd.Run()
	if err != nil {
		return err
	}

	log.Println("Execution completed")

	return nil
}

func main() {
	dir, err := baseDirectory()
	if err != nil {
		log.Println("Failed to get base directoyr:", err)
		return
	}

	// Setup the log file
	logFile, err := os.OpenFile(dir+"\\rewst.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Println("Failed to open log:", err)
		return
	}
	defer logFile.Close()
	log.SetOutput(logFile)

	// Show info
	log.Println("Version:", version.Version)
	log.Println("Running on:", runtime.GOOS)

	// Load the configuration file
	var config Config
	err = load(dir+"\\config.json", &config)
	if err != nil {
		log.Println("Failed to load the config file:", err)
		return
	}
	log.Println("Configuration file loaded")

	// Output the code
	log.Println("Loaded Configuration: ")
	log.Printf("device_id=%s\n", config.DeviceId)
	log.Printf("rewst_org_id=%s\n", config.RewstOrgId)
	log.Printf("rewst_engine_host=%s\n", config.RewstEngineHost)
	log.Printf("shared_access_key=%s\n", config.SharedAccessKey)
	log.Printf("azure_iot_hub_host=%s\n", config.AzureIotHubHost)

	// Check if the window service is run
	isWindowsService, err := svc.IsWindowsService()
	if err != nil {
		log.Println("Failed to determine session type:", err)
		return
	}

	if !isWindowsService {
		// Run as a console application
		fmt.Println("This executable should be run as a Windows service.")
		return
	}

	// Run as a Windows service
	log.Println("Running Windows service")
	service := AgentService{
		Configuration: config,
	}
	svc.Run("AgentSmithGoService", &service)
}
