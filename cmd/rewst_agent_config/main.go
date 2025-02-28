package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/interpreter"
	"github.com/RewstApp/agent-smith-go/internal/mqtt"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
)

type FetchConfigurationResponse struct {
	Configuration agent.Device `json:"configuration"`
}

func getOldFilePath(filePath string) (string, error) {
	oldFilePath := filePath

	for {
		oldFilePath = oldFilePath + "_oldver"

		_, err := os.Stat(oldFilePath)
		if errors.Is(err, os.ErrNotExist) {
			return oldFilePath, nil
		}

		if err != nil {
			return "", nil
		}
	}
}

func moveFileToOld(filePath string) error {
	// Verify first if the file exists
	_, err := os.Stat(filePath)
	if errors.Is(err, os.ErrNotExist) {
		// Do nothing
		return nil
	}

	if err != nil {
		// An error occurred
		return err
	}

	oldFilePath, err := getOldFilePath(filePath)
	if err != nil {
		return err
	}

	// Move file
	log.Println("Moving", filePath, "to", oldFilePath)
	return os.Rename(filePath, oldFilePath)
}

func main() {
	// Show header
	utils.ConfigureLogger("rewst_agent_config", os.Stdout)
	log.Println("Version:", version.Version)
	log.Println("Running on:", runtime.GOOS)

	signalChan := utils.MonitorSignal()

	// Parse command-line arguments
	var configSecret string
	var configUrl string
	var orgId string

	// Arguments are based on the python version
	flag.StringVar(&configSecret, "config-secret", "", "Secret key for configuration access")
	flag.StringVar(&configUrl, "config-url", "", "URL to fetch the configuration from")
	flag.StringVar(&orgId, "org-id", "", "Organization ID to register agent within")
	flag.Parse()

	// Configure logger
	utils.ConfigureLogger("rewst_agent_config", os.Stdout)

	// Validate command-line arguments
	if len(configSecret) == 0 {
		log.Fatalln("Missing config-secret parameter")
	}

	if len(configUrl) == 0 {
		log.Fatalln("Missing config-url parameter")
	}

	if len(orgId) == 0 {
		log.Fatalln("Missing org-id parameter")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Wait for the signal to cancel
		<-signalChan
		log.Println("Signal received")

		cancel()
	}()

	// Get installation paths data
	var pathsData agent.PathsData
	err := pathsData.Load(ctx, orgId)
	if err != nil {
		log.Println(err)
		return
	}

	// Fetch configuration
	hostInfoBytes, err := json.MarshalIndent(pathsData.Tags, "", "  ")
	if err != nil {
		log.Println(err)
		return
	}

	// Modify config url to add the param
	configUrl = fmt.Sprintf("%s?agent_app=agent-smith-go", configUrl)

	// Prepare http request and send
	log.Println("Sending", string(hostInfoBytes), "to", configUrl)

	req, err := http.NewRequestWithContext(ctx, "POST", configUrl, bytes.NewReader(hostInfoBytes))
	if err != nil {
		log.Println(err)
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
		log.Println("Request failed with status code:", res.StatusCode)
		return
	}

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		log.Println(err)
		return
	}

	// Parse the fetch configuration response
	var response FetchConfigurationResponse
	err = json.Unmarshal(bodyBytes, &response)
	if err != nil {
		log.Println(err)
		return
	}

	// Save the configuration file
	configFilePath, err := agent.GetConfigFilePath(orgId)
	if err != nil {
		log.Println(err)
		return
	}

	configBytes, err := json.MarshalIndent(response.Configuration, "", "  ")
	if err != nil {
		log.Println(err)
		return
	}

	// Got configuration
	log.Println("Received configuration:", string(configBytes))

	err = os.WriteFile(configFilePath, configBytes, 0644)
	if err != nil {
		log.Println(err)
		return
	}

	log.Println("Configuration saved to", configFilePath)

	// Rename files to old versions before downloading the new ones
	err = moveFileToOld(pathsData.ServiceExecutablePath)
	if err != nil {
		log.Println(err)
		return
	}

	err = moveFileToOld(pathsData.AgentExecutablePath)
	if err != nil {
		log.Println(err)
		return
	}

	err = moveFileToOld(pathsData.ServiceManagerPath)
	if err != nil {
		log.Println(err)
		return
	}

	// Connect to the receive the device configuration script
	channel := response.Configuration.Subscribe(ctx)

	for event := range channel {
		switch event.Type {
		case mqtt.OnConnecting:
			log.Println("Connecting to broker...")
		case mqtt.OnError:
			log.Println("Error:", event.Error)
		case mqtt.OnSubscribed:
			log.Println("Subscribed to message topic")
		case mqtt.OnConnectionLost:
			log.Println("Connection lost:", event.Error)
		case mqtt.OnCancelled:
			log.Println("Subscription cancelled")
		case mqtt.OnMessageReceived:
			// Execute the payload on a goroutine so it won't block the receiver
			go func() {
				// Parse the message
				var msg interpreter.Message
				err := msg.Parse(event.Message)
				if err != nil {
					log.Println("Parse failed:", err)
					return
				}

				// Execute the command
				err = msg.Execute(ctx, &response.Configuration)
				if err != nil {
					log.Println("Failed to execute message:", err)
					return
				}

				// Only execute one command to install
				if msg.Commands != nil {
					cancel()
				}
			}()
		}
	}

	log.Println("Config closed")
}
