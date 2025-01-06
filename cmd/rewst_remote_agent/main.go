package main

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

	"golang.org/x/text/encoding/unicode"

	"golang.org/x/text/transform"

	"github.com/RewstApp/agent-smith-go/internal/mqtt"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
)

type ExecuteMessage struct {
	PostId              string  `json:"post_id"`
	Commands            string  `json:"commands"`
	InterpreterOverride *string `json:"interpreter_override"`
}

func (m ExecuteMessage) GetCommandBytes() ([]byte, error) {
	content, err := base64.StdEncoding.DecodeString(m.Commands)
	if err != nil {
		return []byte{}, err
	}

	return content, nil
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

	log.SetPrefix("[rewst_remote_agent] ")

	dir, err := utils.BaseDirectory()
	if err != nil {
		log.Println("Failed to get base directory:", err)
		return
	}

	// Setup the log file
	logFile, err := os.OpenFile(filepath.Join(dir, utils.LogFileName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
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
	conf := utils.Config{}
	err = conf.Load(filepath.Join(dir, utils.ConfigFileName))
	if err != nil {
		log.Println("Failed to load the config file:", err)
		return
	}
	log.Println("Configuration file loaded")

	// Output the code
	log.Println("Loaded Configuration: ")
	log.Printf("device_id=%s\n", conf.DeviceId)
	log.Printf("rewst_org_id=%s\n", conf.RewstOrgId)
	log.Printf("rewst_engine_host=%s\n", conf.RewstEngineHost)
	log.Printf("shared_access_key=%s\n", conf.SharedAccessKey)
	log.Printf("azure_iot_hub_host=%s\n", conf.AzureIotHubHost)

	log.Println("Connecting to IoT Hub...")

	messageChan, err := mqtt.SubscribeToAzureIotHub(conf)
	if err != nil {
		log.Println("Failed to connect to Iot Hub:", err)
		return
	}

	// Indicate the service is running
	log.Println("Agent is running...")

	// Main agent loop
	for {
		select {
		case msg := <-messageChan:
			log.Println("Message received:", string(msg))
			if err := Execute(msg); err != nil {
				log.Println("Failed to execute message:", err)
			}
		case <-signalChan:
			// Received signal to stop the agent
			log.Println("Agent is stopping...")
			return
		}
	}
}
