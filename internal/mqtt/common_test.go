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
		t.Fatalf("expected no error, got %v", err)
	}

	if opts.ClientID != device.DeviceId {
		t.Errorf("expected ClientID to be %s, got %s", device.DeviceId, opts.ClientID)
	}

	expectedUsername := device.AzureIotHubHost + "/" + device.DeviceId + "/?api-version=2021-04-12"

	if opts.Username != expectedUsername {
		t.Errorf("expected Username to be %s, got %s", expectedUsername, opts.Username)
	}

	if opts.Password == "" {
		t.Error("expected non-empty Password (SAS token)")
	}

	if len(opts.Servers) == 0 {
		t.Error("expected at least one mqtt broker to be configured")
	}
}
