package mqtt

import (
	"testing"

	"github.com/RewstApp/agent-smith-go/internal/agent"
)

func TestNewClientOptions_DefaultAzureIotHub(t *testing.T) {
	device := agent.Device{
		DeviceId:        "test-device",
		AzureIotHubHost: "myhub.azure-devices.net",
		SharedAccessKey: "c2VjcmV0a2V5", // "secretkey" base64
		Broker:          "",             // triggers default case
	}

	opts, err := NewClientOptions(device)
	if err != nil {
		t.Fatalf("NewClientOptions returned error: %v", err)
	}

	if opts.ClientID != device.DeviceId {
		t.Errorf("Expected ClientID %s, got %s", device.DeviceId, opts.ClientID)
	}

	expectedUsername := device.AzureIotHubHost + "/" + device.DeviceId + "/?api-version=2021-04-12"
	if opts.Username != expectedUsername {
		t.Errorf("Expected Username %s, got %s", expectedUsername, opts.Username)
	}

	if opts.Password == "" {
		t.Error("Expected non-empty Password (SAS token)")
	}

	if len(opts.Servers) == 0 {
		t.Error("Expected at least one MQTT broker to be configured")
	}
}
