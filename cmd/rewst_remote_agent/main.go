package main

import (
	"context"
	"flag"
	"io"
	"log"
	"os"
	"runtime"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/interpreter"
	"github.com/RewstApp/agent-smith-go/internal/mqtt"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
)

func main() {
	// Create a channel to monitor incoming signals to closes
	signalChan := utils.MonitorSignal()

	// Parse command-line arguments
	var configFilePath string
	var logFilePath string

	flag.StringVar(&configFilePath, "config-file", "", "Config file path")
	flag.StringVar(&logFilePath, "log-file", "", "Log file path")
	flag.Parse()

	// Configure logger
	var loggerWriter io.Writer

	// Setup the log file if present
	if len(logFilePath) > 0 {
		logFile, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Println("Failed to open log:", err)
			return
		}
		defer logFile.Close()

		// Use the log file as logger writer
		loggerWriter = logFile
	} else {
		loggerWriter = os.Stdout
	}
	utils.ConfigureLogger("[rewst_remote_agent]", loggerWriter)

	// Show header
	log.Println("Version:", version.Version)
	log.Println("Running on:", runtime.GOOS)

	// Validate command-line arguments
	if len(configFilePath) == 0 {
		log.Println("Missing config-file parameter")
		return
	}

	// Load the configuration file
	device := agent.Device{}
	err := device.Load(configFilePath)
	if err != nil {
		log.Println("Load config file failed:", err)
		return
	}
	log.Println("Configuration file loaded")

	// Output the code
	log.Println("Loaded Configuration: ")
	log.Printf("device_id=%s\n", device.DeviceId)
	log.Printf("rewst_org_id=%s\n", device.RewstOrgId)
	log.Printf("rewst_engine_host=%s\n", device.RewstEngineHost)
	log.Printf("shared_access_key=%s\n", device.SharedAccessKey)
	log.Printf("azure_iot_hub_host=%s\n", device.AzureIotHubHost)
	log.Printf("broker=%v\n", device.Broker)

	// Create context for cancellation
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Run a waiting goroutine for the signal
		<-signalChan

		log.Println("Signal received")
		cancel()
	}()

	// Create the reconnect generator
	rg := utils.ReconnectTimeoutGenerator{}

	for {
		channel := device.Subscribe(ctx)
		reconnect := false

		for ev := range channel {
			switch ev.Type {
			case mqtt.OnMessageReceived:
				// Execute the payload on a goroutine so it won't block the receiver
				go func() {
					var msg interpreter.Message
					err := msg.Parse(ev.Message)
					if err != nil {
						log.Println("Parse failed:", err)
						return
					}

					err = msg.Execute(ctx, &device)
					if err != nil {
						log.Println("Failed to execute message:", err)
						return
					}
				}()
			case mqtt.OnError:
				log.Println("Error:", ev.Error)
				reconnect = true
			case mqtt.OnConnecting:
				log.Println("Connecting to broker...")
			case mqtt.OnConnect:
				log.Println("Connected to broker")
			case mqtt.OnSubscribed:
				log.Println("Subscribed to message topic")

				// Reset the reconnect once subscription is successful
				rg.Clear()
			case mqtt.OnConnectionLost:
				log.Println("Connection lost:", ev.Error)
				reconnect = true
			case mqtt.OnCancelled:
				log.Println("Subscription cancelled")
			}
		}

		// Stop the main loop if reconnect is set as false
		if !reconnect {
			break
		}

		// Wait for timeout or cancelled to happen
		timeout := rg.Next()
		log.Println("Reconnecting in", timeout)

		select {
		case <-time.After(timeout):
			reconnect = true
		case <-ctx.Done():
			reconnect = false
		}

		// Stop the main loop if reconnect is set as false
		if !reconnect {
			break
		}
	}

	log.Println("Closed")
}
