package mqtt

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"log"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/utils"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type AzureIotHubConnection struct {
	client  mqtt.Client
	topic   string
	channel chan []byte
}

func (c AzureIotHubConnection) MessageChannel() <-chan []byte {
	return c.channel
}

func (c AzureIotHubConnection) Close() {
	c.client.Disconnect(250)
	close(c.channel)
	log.Println("Disconnected from Azure IoT Hub")
}

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

func SubscribeToAzureIotHub(config utils.Config) (AzureIotHubConnection, error) {
	// Create a tls connection to broker
	rootCAs, err := utils.RootCAs()
	if err != nil {
		return AzureIotHubConnection{}, err
	}

	// Generate SAS token
	resourceURI := fmt.Sprintf("%s/devices/%s", config.AzureIotHubHost, config.DeviceId)
	sasToken, err := generateSASToken(resourceURI, config.SharedAccessKey, time.Hour)
	if err != nil {
		return AzureIotHubConnection{}, err
	}

	// Initialize MQTT options
	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tls://%s:8883", config.AzureIotHubHost)) // Use port 8883 for MQTT over TLS
	opts.SetClientID(config.DeviceId)
	opts.SetUsername(fmt.Sprintf("%s/%s/?api-version=2021-04-12", config.AzureIotHubHost, config.DeviceId))
	opts.SetPassword(sasToken)
	opts.SetTLSConfig(&tls.Config{
		RootCAs:    rootCAs,
		MinVersion: tls.VersionTLS12,
	}) // Use proper TLS validation in production

	// Create the connection here
	var conn AzureIotHubConnection
	conn.channel = make(chan []byte)

	// Define message handlers
	// TODO: Handle these events properly
	opts.OnConnect = func(client mqtt.Client) {
		log.Println("Connected to Azure IoT Hub!")

		// Subscribe to the cloud-to-device (C2D) message topic
		conn.topic = fmt.Sprintf("devices/%s/messages/devicebound/#", config.DeviceId)
		if token := client.Subscribe(conn.topic, 1, func(client mqtt.Client, msg mqtt.Message) {
			log.Println("Received message on topic:", msg.Topic(), string(msg.Payload()))
			conn.channel <- msg.Payload()
		}); token.Wait() && token.Error() != nil {
			log.Println("Failed to subscribe to topic:", token.Error())
			conn.Close()
			return
		}
		log.Println("Subscribed to C2D message topic.")
	}
	opts.OnConnectionLost = func(client mqtt.Client, err error) {
		log.Println("Connection lost:", err)
		conn.Close()
	}

	// Create and connect the MQTT client
	client := mqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		return AzureIotHubConnection{}, token.Error()
	}
	conn.client = client

	return conn, nil
}
