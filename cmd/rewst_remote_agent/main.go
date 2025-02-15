package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"

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

	channel := mqtt.Subscribe(conf, ctx)

	for ev := range channel {
		switch ev.Type {
		case mqtt.OnMessageReceived:
			// Received message
			log.Println("Received message:", ev.Message)

			// Execute the payload
			if err := interpreter.Execute(ev.Message, &conf); err != nil {
				log.Println("Failed to execute message:", err)
			}
		case mqtt.OnError:
			log.Println("Error occurred:", ev.Error)
		case mqtt.OnConnecting:
			log.Println("Connecting to broker...")
		case mqtt.OnConnect:
			log.Println("Connected to broker")
		case mqtt.OnSubscribed:
			log.Println("Subscribed to message topic")
		case mqtt.OnConnectionLost:
			log.Println("Connection lost:", ev.Error)
		case mqtt.OnCancelled:
			log.Println("Subscription cancelled")
		}
	}

	log.Println("Agent closed")

	log.Println("Go Routines:", runtime.NumGoroutine())

	/*
		rg := utils.ReconnectTimeoutGenerator{}
			for {

				// TODO: Capture signal anywhere in the process here

				conn, err := mqtt.Subscribe(context.Background(), conf)
				if err != nil {
					log.Println("Failed to connect to Iot Hub:", err)

					timeout := rg.Next()
					log.Println("Reconnecting in", timeout)
					time.Sleep(timeout)
					continue
				}

				// Indicate the service is running
				rg.Clear()
				log.Println("Agent is running...")

				// Main agent loop
			agent_loop:
				for {
					select {
					case msg, ok := <-conn.MessageChannel():
						if !ok {
							// Channel is closed
							// TODO: Establish a reconnection process
							log.Println("Disconnected")
							break agent_loop
						}

						log.Println("Message received:", string(msg))
						if err := interpreter.Execute(msg, &conf); err != nil {
							log.Println("Failed to execute message:", err)
						}
					case <-signalChan:
						// Received signal to stop the agent
						// TODO: Notify MQTT about client initiated shutdown
						log.Println("Agent is stopping...")
						conn.Close()
						log.Println("Agent stopped")
						return
					}
				}

				// Loop broken, reconnect
				timeout := rg.Next()
				log.Println("Reconnecting in", timeout)
				time.Sleep(timeout)
			}*/
}
