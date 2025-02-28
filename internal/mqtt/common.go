package mqtt

import (
	"github.com/RewstApp/agent-smith-go/internal/agent"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type EventType int

const (
	OnError = iota
	OnMessageReceived
	OnConnecting
	OnConnect
	OnConnectionLost
	OnSubscribed
	OnCancelled
)

type Event struct {
	Type    EventType
	Message []byte
	Error   error
}

type Client = mqtt.Client
type Message = mqtt.Message

var NewClient = mqtt.NewClient

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
