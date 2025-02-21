package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"io"
	"log"
	"net/http"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/interpreter"
	"github.com/RewstApp/agent-smith-go/internal/mqtt"
	"github.com/RewstApp/agent-smith-go/internal/utils"
)

type FetchConfigurationResponse struct {
	Configuration agent.Device `json:"configuration"`
}

func main() {
	signalChan := utils.MonitorSignal()

	// Parse command-line arguments
	var configSecret string
	var configUrl string
	var orgId string

	flag.StringVar(&configSecret, "config-secret", "", "Secret key for configuration access")
	flag.StringVar(&configUrl, "config-url", "", "URL to fetch the configuration from")
	flag.StringVar(&orgId, "org-id", "", "Organization ID to register agent within")
	flag.Parse()

	log.SetPrefix("[rewst_agent_config] ")

	// Validate command-line arguments
	if len(configSecret) == 0 {
		log.Fatalln("Error: Missing config-secret parameter")
	}

	if len(configUrl) == 0 {
		log.Fatalln("Error: Missing config-url parameter")
	}

	if len(orgId) == 0 {
		log.Fatalln("Error: Missing org-id parameter")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		// Wait for the signal to cancel
		<-signalChan
		log.Println("Signal received")

		cancel()
	}()

	// Build the host info
	var hostInfo agent.HostInfo
	err := hostInfo.Load(ctx, orgId)
	if err != nil {
		log.Fatal(err)
	}

	hostInfoBytes, err := json.Marshal(hostInfo)
	if err != nil {
		log.Fatal(err)
	}

	// Fetch configuration
	// Prepare http request and send
	log.Println("Sending")
	log.Println(string(hostInfoBytes))
	log.Println("to", configUrl)

	req, err := http.NewRequestWithContext(ctx, "POST", configUrl, bytes.NewReader(hostInfoBytes))
	if err != nil {
		log.Fatal(err)
	}
	req.Header.Set("x-rewst-secret", configSecret)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil {
		log.Fatal(err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		log.Fatalln("Request failed with status code:", res.StatusCode)
	}

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		log.Fatal(err)
	}

	// Parse the fetch configuration response
	var response FetchConfigurationResponse
	err = json.Unmarshal(bodyBytes, &response)
	if err != nil {
		log.Fatal(err)
	}

	// Got configuration
	log.Println("Received configuration:")
	log.Println(string(bodyBytes))

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
				result, err := msg.Execute(ctx, &response.Configuration)
				if err != nil {
					log.Println("Failed to execute message:", err)
					return
				}

				// Display results
				if result.Commands != nil {
					log.Println("Commands saved to temp file:", result.Commands.TempFilename)
					log.Println("Commands", result.PostId, "executed using", result.Commands.Interpreter, "with status code", result.Commands.ExitCode)
					log.Println("Stderr:", result.Commands.Stderr)
					log.Println("Stdout:", result.Commands.Stdout)
				}

				if result.GetInstallation != nil {
					log.Println("Installation data sent with status code:", result.GetInstallation.StatusCode)
				}

			}()
		}
	}
}
