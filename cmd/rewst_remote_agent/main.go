package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/interpreter"
	"github.com/RewstApp/agent-smith-go/internal/mqtt"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
)

func main() {
	// Create a channel to monitor incoming signals to closes
	signalChan := make(chan os.Signal, 1)

	if runtime.GOOS == "windows" {
		// Windows only supports os.Interrupt signal
		signal.Notify(signalChan, os.Interrupt)
	} else {
		signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	}

	// Parse command-line arguments
	var configFilePath string
	var logFilePath string

	flag.StringVar(&configFilePath, "config", "", "Config file path")
	flag.StringVar(&logFilePath, "log", "", "Log file path")
	flag.Parse()

	log.SetPrefix("[rewst_remote_agent] ")

	// Setup the log file if present
	if len(logFilePath) > 0 {
		logFile, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Println("Failed to open log:", err)
			return
		}
		defer logFile.Close()
		log.SetOutput(logFile)
	}

	// Load the configuration file
	conf := utils.Config{}
	err := conf.Load(configFilePath)
	if err != nil {
		log.Println("Load config file failed:", err)
		return
	}
	log.Println("Configuration file loaded")

	// Show info
	log.Println("Version:", version.Version)
	log.Println("Running on:", runtime.GOOS)

	// Output the code
	log.Println("Loaded Configuration: ")
	log.Printf("device_id=%s\n", conf.DeviceId)
	log.Printf("rewst_org_id=%s\n", conf.RewstOrgId)
	log.Printf("rewst_engine_host=%s\n", conf.RewstEngineHost)
	log.Printf("shared_access_key=%s\n", conf.SharedAccessKey)
	log.Printf("azure_iot_hub_host=%s\n", conf.AzureIotHubHost)
	log.Printf("broker=%v\n", conf.Broker)

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
		channel := mqtt.Subscribe(conf, ctx)
		reconnect := false

		for ev := range channel {
			switch ev.Type {
			case mqtt.OnMessageReceived:
				// Execute the payload on a goroutine so it won't block the receiver
				go func() {
					result, err := interpreter.Execute(ev.Message)
					if err != nil {
						log.Println("Failed to execute message:", err)
						return
					}

					// Display results
					log.Println("Commands saved to temp file:", result.TempFilename)
					log.Println("Commands", result.PostId, "executed using", result.Interpreter, "with status code", result.ExitCode)
					log.Println("Stderr:", result.Stderr)
					log.Println("Stdout:", result.Stdout)
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
