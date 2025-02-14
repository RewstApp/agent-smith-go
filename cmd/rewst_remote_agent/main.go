package main

import (
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

	// Refactor the code below
	// FREAKING SIMPLIFY IT!
	log.Println("Connecting to IoT Hub...")

	stopChannel := make(chan struct{})

	// Run a waiting goroutine for the signal
	go func() {
		<-signalChan
		stopChannel <- struct{}{}
	}()

	channel := mqtt.Subscribe(conf, stopChannel)

	for msg := range channel {
		// Error occured
		if msg.Error != nil {
			log.Printf("An error occured: %v\n", msg.Error)
			return
		}

		// Execute the payload
		if err := interpreter.Execute(msg.Payload, &conf); err != nil {
			log.Println("Failed to execute message:", err)
		}
	}

	log.Println("Agent closed")

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
