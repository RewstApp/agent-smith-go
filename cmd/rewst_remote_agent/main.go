package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"github.com/RewstApp/agent-smith-go/pkg/version"

	"golang.org/x/text/encoding/unicode"

	"golang.org/x/text/transform"

	"github.com/amenzhinsky/iothub/iotdevice"
	iotmqtt "github.com/amenzhinsky/iothub/iotdevice/transport/mqtt"
)

type ExecuteMessage struct {
	PostId              string  `json:"post_id"`
	Commands            string  `json:"commands"`
	InterpreterOverride *string `json:"interpreter_override"`
}

func (m ExecuteMessage) GetCommandBytes() ([]byte, error) {
	content, err := base64.StdEncoding.DecodeString(m.Commands)
	if err != nil {
		return []byte{}, nil
	}

	return content, nil
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
	commandBytes, err := message.GetCommandBytes()
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
	shell := "powershell"
	if runtime.GOOS != "windows" {
		shell = "pwsh"
	}

	cmd := exec.Command(shell, "-Command", commands)
	err = cmd.Run()
	if err != nil {
		return err
	}

	log.Println("Execution completed")

	return nil
}

func main() {
	// Create a channel to monitor incoming signals to closes
	signalChan := make(chan os.Signal, 1)

	if runtime.GOOS == "windows" {
		// Windows only supports os.Interrupt signal
		signal.Notify(signalChan, os.Interrupt)
	} else {
		signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	}

	dir, err := baseDirectory()
	if err != nil {
		log.Println("Failed to get base directory:", err)
		return
	}

	// Setup the log file
	logFile, err := os.OpenFile(dir+"//rewst.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
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
	err = load(dir+"//config.json", &config)
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

	// Run the agent here
	connStr := config.ConnectionString()
	log.Println("Connecting to Iot Hub:", connStr)

	// Create a new device client
	client, err := iotdevice.NewFromConnectionString(iotmqtt.New(), config.ConnectionString())
	if err != nil {
		log.Println("Failed to create client:", err)
		return
	}
	log.Println("Client created")

	// Connect to IoT Hub
	if err = client.Connect(context.Background()); err != nil {
		log.Println("Failed to connect to Iot Hub:", err)
		return
	}
	log.Println("Connected to Iot Hub")

	// Indicate the service is running
	log.Println("Agent is running...")

	// Subscribe to events
	sub, err := client.SubscribeEvents(context.Background())
	if err != nil {
		log.Println("Failed to subscribe events:", err)
		return
	}
	log.Println("Subscribed to events")

	// Main agent loop
	for {
		select {
		case msg := <-sub.C():
			if err = Execute(msg.Payload); err != nil {
				log.Println("Failed to execute message:", err)
			}
		case <-signalChan:
			// Received signal to stop the agent
			log.Println("Agent is stopping...")

			if err = client.Close(); err != nil {
				log.Println("Closing client failed:", err)
				return
			}

			log.Println("Client closed")
			return
		}
	}
}
