package mqtt

import (
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type Client = mqtt.Client
type Message = mqtt.Message

var NewClient = mqtt.NewClient

const DefaultDisconnectQuiesce time.Duration = 250 * time.Millisecond

func NewClientOptions(device agent.Device) (*mqtt.ClientOptions, error) {
	switch device.Broker {
	default:
		return newAzureIotHubClientOptions(azureIotHubDevice{
			DeviceId:        device.DeviceId,
			Host:            device.AzureIotHubHost,
			SharedAccessKey: device.SharedAccessKey,
		})
	}
}
