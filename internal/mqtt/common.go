package mqtt

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type (
	Client  = mqtt.Client
	Message = mqtt.Message
)

var NewClient = mqtt.NewClient

const DefaultDisconnectQuiesce time.Duration = 250 * time.Millisecond

func NewClientOptions(device agent.Device) (*mqtt.ClientOptions, error) {
	var (
		opts *mqtt.ClientOptions
		err  error
	)

	switch device.Broker {
	default:
		opts, err = newAzureIotHubClientOptions(azureIotHubDevice{
			DeviceId:        device.DeviceId,
			Host:            device.AzureIotHubHost,
			SharedAccessKey: device.SharedAccessKey,
		})
	}

	if err != nil {
		return nil, err
	}

	// Explicitly own the connect timeout instead of relying on paho's implicit
	// 30s default, so reconnect timing stays predictable across paho upgrades.
	// See utils.DefaultMqttConnectTimeout for the value and rationale.
	opts.SetConnectTimeout(device.MqttConnectTimeout())

	return opts, nil
}

type ReportedProperties struct {
	AgentVersion string `json:"agent_version"`
}

// UpdateReportedProperties publishes reported properties to the Azure IoT Hub device twin.
func UpdateReportedProperties(client mqtt.Client, props ReportedProperties) error {
	payload, err := json.Marshal(props)
	if err != nil {
		return fmt.Errorf("failed to marshal reported properties: %w", err)
	}

	topic := "$iothub/twin/PATCH/properties/reported/?$rid=1"
	token := client.Publish(topic, 0, false, payload)
	token.Wait()
	if token.Error() != nil {
		return fmt.Errorf("failed to publish reported properties: %w", token.Error())
	}

	return nil
}
