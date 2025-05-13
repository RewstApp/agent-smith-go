package mqtt

import (
	"strings"
	"testing"
	"time"
)

func TestGenerateSASToken(t *testing.T) {
	resourceURI := "my-iot-hub.azure-devices.net/devices/mydevice"
	key := "c2VjcmV0a2V5" // "secretkey" in base64

	token, err := generateSASToken(resourceURI, key, 1*time.Hour)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !strings.HasPrefix(token, "SharedAccessSignature") {
		t.Errorf("expected token to start with 'SharedAccessSignature', got %s", token)
	}

	if !strings.Contains(token, "sr="+resourceURI) {
		t.Errorf("expected to have sr=URI, got %s", token)
	}

	if !strings.Contains(token, "sig=") {
		t.Errorf("expected to have sig=, got %s", token)
	}

	if !strings.Contains(token, "se=") {
		t.Errorf("expected to have se=, got %s", token)
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
		t.Fatalf("expected no error, got %v", err)
	}

	if opts.ClientID != device.DeviceId {
		t.Errorf("expected ClientID to be %s, got %s", device.DeviceId, opts.ClientID)
	}

	expectedUsername := device.Host + "/" + device.DeviceId + "/?api-version=2021-04-12"
	if opts.Username != expectedUsername {
		t.Errorf("expected Username to be %s, got %s", expectedUsername, opts.Username)
	}

	if opts.Password == "" {
		t.Errorf("expected Password (SAS token) to be set")
	}

	if opts.TLSConfig == nil {
		t.Errorf("expected TLS config to be set")
	}
}
