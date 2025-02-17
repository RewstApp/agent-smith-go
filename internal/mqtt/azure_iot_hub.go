package mqtt

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/utils"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// generateSASToken generates a SAS token for Azure IoT Hub
func generateSASToken(resourceURI, key string, duration time.Duration) (string, error) {
	// Set expiration time
	expiration := time.Now().Add(duration).Unix()

	// Create the string to sign
	stringToSign := fmt.Sprintf("%s\n%d", resourceURI, expiration)

	// Decode the base64 key
	keyBytes, err := base64.StdEncoding.DecodeString(key)
	if err != nil {
		return "", fmt.Errorf("Failed to decode key: %w", err)
	}

	// Create the HMAC-SHA256 signature
	h := hmac.New(sha256.New, keyBytes)
	h.Write([]byte(stringToSign))
	signature := base64.StdEncoding.EncodeToString(h.Sum(nil))

	// Create the SAS token
	token := fmt.Sprintf("SharedAccessSignature sr=%s&sig=%s&se=%d", resourceURI, signature, expiration)
	return token, nil
}

func subscribeToAzureIotHub(config utils.Config, ctx context.Context) <-chan Event {
	// Create the channels for the subscription
	channel := make(chan Event)

	go func() {
		defer close(channel)

		childCtx, cancel := context.WithCancel(ctx)

		// Create a tls connection to broker
		rootCAs, err := utils.RootCAs()
		if err != nil {
			channel <- Event{OnError, nil, err}
			cancel()
			return
		}

		// Generate SAS token
		resourceURI := fmt.Sprintf("%s/devices/%s", config.AzureIotHubHost, config.DeviceId)
		sasToken, err := generateSASToken(resourceURI, config.SharedAccessKey, time.Hour)
		if err != nil {
			channel <- Event{OnError, nil, err}
			cancel()
			return
		}

		// Initialize MQTT options
		opts := mqtt.NewClientOptions()
		opts.AddBroker(fmt.Sprintf("tls://%s:8883", config.AzureIotHubHost)) // Use port 8883 for MQTT over TLS
		opts.AddBroker(fmt.Sprintf("wss://%s", config.AzureIotHubHost))      // Add websocket as a backup for Azure Iot Hub
		opts.SetClientID(config.DeviceId)
		opts.SetUsername(fmt.Sprintf("%s/%s/?api-version=2021-04-12", config.AzureIotHubHost, config.DeviceId))
		opts.SetPassword(sasToken)
		opts.SetTLSConfig(&tls.Config{
			RootCAs:       rootCAs,
			MinVersion:    tls.VersionTLS12,
			Renegotiation: tls.RenegotiateOnceAsClient,
		}) // Use proper TLS validation in production
		opts.SetAutoReconnect(false)

		// Handle the case when the connection was lost
		opts.OnConnectionLost = func(client mqtt.Client, err error) {
			go func() {
				channel <- Event{OnConnectionLost, nil, err}
				cancel()
			}()
		}

		// Define message handlers
		opts.OnConnect = func(client mqtt.Client) {
			go func() {
				// Trigger on connect events
				channel <- Event{OnConnect, nil, nil}

				// Subscribe to the cloud-to-device (C2D) message topic
				topic := fmt.Sprintf("devices/%s/messages/devicebound/#", config.DeviceId)
				token := client.Subscribe(topic, 1, func(client mqtt.Client, msg mqtt.Message) {
					go func() {
						channel <- Event{OnMessageReceived, msg.Payload(), err}
					}()
				})

				cancelled, err := waitToken(token, ctx)

				if cancelled {
					channel <- Event{OnCancelled, nil, err}
					cancel()
					return
				}

				if err != nil {
					// Failed to subscribe to message topic
					channel <- Event{OnError, nil, err}
					cancel()
					return
				}

				// Trigger on subscribed event
				channel <- Event{OnSubscribed, nil, nil}
			}()
		}

		// Create and connect the MQTT client
		client := mqtt.NewClient(opts)

		channel <- Event{OnConnecting, nil, nil}

		token := client.Connect()
		defer client.Disconnect(250)

		cancelled, err := waitToken(token, ctx)

		if cancelled {
			channel <- Event{OnCancelled, nil, err}
			cancel()
			return
		}

		if err != nil {
			// Failed to connect
			channel <- Event{OnError, nil, err}
			cancel()
			return
		}

		// Wait for the context to be cancelled
		<-childCtx.Done()
	}()

	return channel
}
