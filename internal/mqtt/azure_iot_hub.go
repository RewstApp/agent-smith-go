package mqtt

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type azureIotHubDevice struct {
	DeviceId        string
	Host            string
	SharedAccessKey string
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

func newAzureIotHubClientOptions(device azureIotHubDevice) (*mqtt.ClientOptions, error) {
	// Generate SAS token
	resourceURI := fmt.Sprintf("%s/devices/%s", device.Host, device.DeviceId)
	sasToken, err := generateSASToken(resourceURI, device.SharedAccessKey, time.Hour)
	if err != nil {
		return nil, err
	}

	// Initialize MQTT options
	opts := mqtt.NewClientOptions()
	opts.AddBroker(fmt.Sprintf("tls://%s:8883", device.Host)) // Use port 8883 for MQTT over TLS
	opts.AddBroker(fmt.Sprintf("wss://%s", device.Host))      // Add websocket as a backup for Azure Iot Hub
	opts.SetClientID(device.DeviceId)
	opts.SetUsername(fmt.Sprintf("%s/%s/?api-version=2021-04-12", device.Host, device.DeviceId))
	opts.SetPassword(sasToken)
	opts.SetTLSConfig(&tls.Config{
		Renegotiation: tls.RenegotiateOnceAsClient,
	})

	return opts, nil
}
