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
		return "", fmt.Errorf("failed to decode key: %w", err)
	}

	// Create the HMAC-SHA256 signature
	h := hmac.New(sha256.New, keyBytes)
	h.Write([]byte(stringToSign))
	signature := base64.StdEncoding.EncodeToString(h.Sum(nil))

	// Create the SAS token
	token := fmt.Sprintf("SharedAccessSignature sr=%s&sig=%s&se=%d", resourceURI, signature, expiration)
	return token, nil
}

func onError(channel chan<- Event, err error) {
	channel <- Event{OnError, nil, err}
	close(channel)
}

func onConnectionLost(channel chan<- Event, err error) {
	channel <- Event{OnConnectionLost, nil, err}
	close(channel)
}

func onCancelled(channel chan<- Event, err error) {
	channel <- Event{OnCancelled, nil, err}
	close(channel)
}

func onConnecting(channel chan<- Event) {
	channel <- Event{OnConnecting, nil, nil}
}

func onConnect(channel chan<- Event) {
	channel <- Event{OnConnect, nil, nil}
}

func onSubscribed(channel chan<- Event) {
	channel <- Event{OnSubscribed, nil, nil}
}

func onMessageReceived(channel chan<- Event, payload []byte) {
	channel <- Event{OnMessageReceived, payload, nil}
}

func disconnect(client mqtt.Client) {
	client.Disconnect(250)
}

func waitToken(token mqtt.Token, ctx context.Context) (bool, error) {
	select {
	case <-token.Done():
		// Token completed before cancelling
		return false, token.Error()
	case <-ctx.Done():
		// Cancelled before the token is done
		return true, ctx.Err()
	}
}

func subscribeToAzureIotHub(config utils.Config, ctx context.Context) <-chan Event {
	// Create the channels for the subscription
	channel := make(chan Event)
	stop := make(chan struct{})

	go func() {
		// Create a tls connection to broker
		rootCAs, err := utils.RootCAs()
		if err != nil {
			onError(channel, err)
			return
		}

		// Generate SAS token
		resourceURI := fmt.Sprintf("%s/devices/%s", config.AzureIotHubHost, config.DeviceId)
		sasToken, err := generateSASToken(resourceURI, config.SharedAccessKey, time.Hour)
		if err != nil {
			onError(channel, err)
			return
		}

		// Initialize MQTT options
		opts := mqtt.NewClientOptions()
		opts.AddBroker(fmt.Sprintf("tls://%s:8883", config.AzureIotHubHost)) // Use port 8883 for MQTT over TLS
		opts.AddBroker(fmt.Sprintf("wss://%s", config.AzureIotHubHost))
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
				onConnectionLost(channel, err)

				// Send this signal to stop waiting for messages
				stop <- struct{}{}
			}()
		}

		// Define message handlers
		opts.OnConnect = func(client mqtt.Client) {
			go func() {
				// Trigger on connect event
				onConnect(channel)

				// Subscribe to the cloud-to-device (C2D) message topic
				topic := fmt.Sprintf("devices/%s/messages/devicebound/#", config.DeviceId)
				token := client.Subscribe(topic, 1, func(client mqtt.Client, msg mqtt.Message) {
					go onMessageReceived(channel, msg.Payload())
				})

				cancelled, err := waitToken(token, ctx)

				if cancelled {
					disconnect(client)
					onCancelled(channel, err)
					return
				}

				if err != nil {
					// Failed to subscribe to message topic
					onError(channel, err)
					disconnect(client)
					return
				}

				// Trigger on subscribed event
				onSubscribed(channel)

				// Wait for stop or cancel signal
				select {
				case <-ctx.Done():
					disconnect(client)
					onCancelled(channel, ctx.Err())
				case <-stop:
					disconnect(client)
				}
			}()
		}

		// Create and connect the MQTT client
		client := mqtt.NewClient(opts)
		onConnecting(channel)
		token := client.Connect()
		cancelled, err := waitToken(token, ctx)

		if cancelled {
			disconnect(client)
			onCancelled(channel, err)
			return
		}

		if err != nil {
			// Failed to connect
			onError(channel, err)
			disconnect(client)
			return
		}
	}()

	return channel
}
