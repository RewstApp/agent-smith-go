package mqtt

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/utils"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type azureIotHubDevice struct {
	DeviceId        string
	Host            string
	SharedAccessKey string
	// TokenLifetime is how long the minted SAS token is valid. When zero the
	// documented default (utils.DefaultSasTokenLifetime) is used.
	TokenLifetime time.Duration
	// Proxy is the outbound proxy the WebSocket transport dials through; nil
	// means http.ProxyFromEnvironment (sc-129691).
	Proxy utils.ProxyFunc
}

// WebSocketBrokerURL is the address of IoT Hub's MQTT-over-WebSocket endpoint.
// IoT Hub serves it only at /$iothub/websocket on 443.
func WebSocketBrokerURL(host string) string {
	return fmt.Sprintf("wss://%s:443/$iothub/websocket", host)
}

// TLSBrokerURL is the address of IoT Hub's MQTT-over-TLS endpoint.
func TLSBrokerURL(host string) string {
	return fmt.Sprintf("tls://%s:8883", host)
}

// Transport names how a broker URL connects, for logs and the diagnostic.
func Transport(broker *url.URL) string {
	if broker == nil {
		return "unknown"
	}
	switch broker.Scheme {
	case "ws", "wss":
		return "MQTT over WebSocket (443)"
	default:
		return "MQTT over TLS (8883)"
	}
}

// BrokerProxy reports the proxy a connection to broker goes through: only the
// WebSocket transport can traverse an HTTP proxy, so MQTT over TLS is always
// direct.
func BrokerProxy(proxy utils.ProxyFunc, broker *url.URL) *url.URL {
	if broker == nil || (broker.Scheme != "wss" && broker.Scheme != "ws") {
		return nil
	}
	target := *broker
	target.Scheme = map[string]string{"wss": "https", "ws": "http"}[broker.Scheme]
	return utils.ProxyFor(proxy, &target)
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
	token := fmt.Sprintf(
		"SharedAccessSignature sr=%s&sig=%s&se=%d",
		resourceURI,
		signature,
		expiration,
	)
	return token, nil
}

func newAzureIotHubClientOptions(device azureIotHubDevice) (*mqtt.ClientOptions, error) {
	// Generate SAS token. Fall back to the documented default lifetime when the
	// caller left it unset so the token is never minted with a zero (already
	// expired) expiry.
	lifetime := device.TokenLifetime
	if lifetime <= 0 {
		lifetime = utils.DefaultSasTokenLifetime
	}
	resourceURI := fmt.Sprintf("%s/devices/%s", device.Host, device.DeviceId)
	sasToken, err := generateSASToken(resourceURI, device.SharedAccessKey, lifetime)
	if err != nil {
		return nil, err
	}

	// Initialize MQTT options. MQTT over TLS on 8883 comes first and the
	// WebSocket endpoint on 443 is the fallback for networks that block 8883 -
	// unless a proxy applies to the WebSocket endpoint: 8883 cannot traverse an
	// HTTP proxy, so a proxied network tries the WebSocket through it first
	// (sc-129691). The WebSocket used to be registered as a bare wss://<hub>,
	// a path IoT Hub does not serve.
	proxy := device.Proxy
	if proxy == nil {
		proxy = http.ProxyFromEnvironment
	}
	tlsBroker, wsBroker := TLSBrokerURL(device.Host), WebSocketBrokerURL(device.Host)
	opts := mqtt.NewClientOptions()
	wsURL, err := url.Parse(wsBroker)
	if err != nil {
		return nil, fmt.Errorf("invalid IoT Hub host %q: %w", device.Host, err)
	}
	if BrokerProxy(proxy, wsURL) != nil {
		opts.AddBroker(wsBroker)
		opts.AddBroker(tlsBroker)
	} else {
		opts.AddBroker(tlsBroker)
		opts.AddBroker(wsBroker)
	}
	opts.SetWebsocketOptions(&mqtt.WebsocketOptions{Proxy: proxy})
	opts.SetClientID(device.DeviceId)
	opts.SetUsername(fmt.Sprintf("%s/%s/?api-version=2021-04-12", device.Host, device.DeviceId))
	opts.SetPassword(sasToken)
	opts.SetTLSConfig(&tls.Config{
		Renegotiation: tls.RenegotiateOnceAsClient,
		MinVersion:    tls.VersionTLS12,
	})

	return opts, nil
}
