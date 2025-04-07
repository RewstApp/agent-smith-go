package mqtt

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateSASToken(t *testing.T) {
	resourceURI := "my-iot-hub.azure-devices.net/devices/mydevice"
	// Fake base64-encoded key ("secretkey")
	key := "c2VjcmV0a2V5" // "secretkey" in base64

	token, err := generateSASToken(resourceURI, key, 1*time.Hour)
	if err != nil {
		t.Fatalf("generateSASToken returned error: %v", err)
	}

	if !strings.HasPrefix(token, "SharedAccessSignature") {
		t.Errorf("Expected token to start with 'SharedAccessSignature', got: %s", token)
	}

	if !strings.Contains(token, "sr="+resourceURI) {
		t.Errorf("Token missing resource URI: %s", token)
	}

	if !strings.Contains(token, "sig=") {
		t.Errorf("Token missing signature: %s", token)
	}

	if !strings.Contains(token, "se=") {
		t.Errorf("Token missing expiry: %s", token)
	}
}

func TestNewAzureIotHubClientOptions(t *testing.T) {
	device := azureIotHubDevice{
		DeviceId:        "testdevice",
		Host:            "testhub.azure-devices.net",
		SharedAccessKey: "c2VjcmV0a2V5", // "secretkey" in base64
	}

	opts, err := newAzureIotHubClientOptions(device)
	if err != nil {
		t.Fatalf("newAzureIotHubClientOptions returned error: %v", err)
	}

	if opts.ClientID != device.DeviceId {
		t.Errorf("Expected ClientID to be %s, got %s", device.DeviceId, opts.ClientID)
	}

	expectedUsername := device.Host + "/" + device.DeviceId + "/?api-version=2021-04-12"
	if opts.Username != expectedUsername {
		t.Errorf("Expected Username to be %s, got %s", expectedUsername, opts.Username)
	}

	if opts.Password == "" {
		t.Errorf("Expected Password (SAS token) to be set")
	}

	if opts.TLSConfig == nil {
		t.Errorf("Expected TLS config to be set")
	}
}
