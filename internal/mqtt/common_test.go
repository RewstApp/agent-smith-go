package mqtt

import (
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
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

	// ConnectTimeout must be explicitly owned by us, not paho's implicit 30s
	// default, and default to the documented value.
	if opts.ConnectTimeout != utils.DefaultMqttConnectTimeout {
		t.Errorf(
			"expected ConnectTimeout to default to %v, got %v",
			utils.DefaultMqttConnectTimeout,
			opts.ConnectTimeout,
		)
	}

	// Keepalive/ping must be configured explicitly (not paho's implicit default)
	// so a marginal connection is detected in a bounded, predictable time.
	if opts.KeepAlive != int64(utils.DefaultMqttKeepAlive.Seconds()) {
		t.Errorf(
			"expected KeepAlive to default to %v seconds, got %v",
			int64(utils.DefaultMqttKeepAlive.Seconds()),
			opts.KeepAlive,
		)
	}

	if opts.PingTimeout != utils.DefaultMqttPingTimeout {
		t.Errorf(
			"expected PingTimeout to default to %v, got %v",
			utils.DefaultMqttPingTimeout,
			opts.PingTimeout,
		)
	}
}

func TestNewClientOptions_ConnectTimeoutOverride(t *testing.T) {
	override := 12
	device := agent.Device{
		DeviceId:                  "test-device",
		AzureIotHubHost:           "myhub.azure-devices.net",
		SharedAccessKey:           "c2VjcmV0a2V5",
		Broker:                    "",
		MqttConnectTimeoutSeconds: &override,
	}

	opts, err := NewClientOptions(device)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if opts.ConnectTimeout != 12*time.Second {
		t.Errorf(
			"expected ConnectTimeout to honor per-device override (12s), got %v",
			opts.ConnectTimeout,
		)
	}
}
