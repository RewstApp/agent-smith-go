package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
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
	log.Printf("broker=%v\n", conf.Broker)

	log.Println("Go Routines:", runtime.NumGoroutine())

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
				// Received message
				log.Println("Received message:", ev.Message)

				// Execute the payload
				go func() {
					err := interpreter.Execute(ev.Message, &conf)
					if err != nil {
						log.Println("Failed to execute message:", err)
					}
				}()
			case mqtt.OnError:
				log.Println("Error occurred:", ev.Error)
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

	log.Println("Agent closed")

	log.Println("Go Routines:", runtime.NumGoroutine())
}
