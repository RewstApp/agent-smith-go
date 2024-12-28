package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/svc"

	"github.com/amenzhinsky/iothub/iotdevice"
	iotmqtt "github.com/amenzhinsky/iothub/iotdevice/transport/mqtt"
)

type AgentService struct {
	Configuration Config
}

// Execute is the main entry point for your service logic
func (m *AgentService) Execute(args []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {

	// Notify Windows that the service is starting
	status <- svc.Status{State: svc.StartPending}

	// TODO: Perform the initialization step

	connStr := m.Configuration.ConnectionString()
	log.Println("Connecting to Iot Hub: ", connStr)

	// Create a new device client
	client, err := iotdevice.NewFromConnectionString(iotmqtt.New(), m.Configuration.ConnectionString())
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}
	log.Println("Client created")

	// Connect to IoT Hub
	if err = client.Connect(context.Background()); err != nil {
		log.Fatalf("Failed to connect to Iot Hub: %v", err)
	}
	log.Println("Connected to Iot Hub")

	// Indicate the service is running
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	log.Println("Service is running...")

	// Subscribe to events
	sub, err := client.SubscribeEvents(context.Background())
	if err != nil {
		log.Fatalf("Failed to subscribe events: %v", err)
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
				}
				log.Println("Client closed")

				status <- svc.Status{State: svc.Stopped}

				return true, 0
			}
		case msg := <-sub.C():
			log.Println(msg.To, string(msg.Payload))
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

func baseDirectory() string {
	// Get the path of the current executable
	exePath, err := os.Executable()
	if err != nil {
		log.Fatalf("Failed to get executable path")
	}

	// Get the directory from the executable path
	return filepath.Dir(exePath)
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

func main() {
	dir := baseDirectory()

	// Setup the log file
	logFile, err := os.OpenFile(dir+"\\rewst.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal(err)
	}
	defer logFile.Close()
	log.SetOutput(logFile)

	// Load the configuration file
	var config Config
	err = load(dir+"\\config.json", &config)
	if err != nil {
		log.Fatalf("Failed to load the config file: %v", err)
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
		log.Fatalf("Failed to determine session type: %v", err)
	}

	if isWindowsService {
		// Run as a Windows service
		log.Println("Running Windows service")
		service := AgentService{
			Configuration: config,
		}
		svc.Run("AgentSmithGoService", &service)
	} else {
		// Run as a console application
		log.Println("Running interactively. This is not a Windows service.")
	}
}
