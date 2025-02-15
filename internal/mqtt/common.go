package mqtt

import (
	"context"

	"github.com/RewstApp/agent-smith-go/internal/utils"
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

func Subscribe(config utils.Config, ctx context.Context) <-chan Event {
	switch config.Broker {
	// TODO: Support other brokers here
	default:
		// Azure IoT Hub is the default
		return subscribeToAzureIotHub(config, ctx)
	}
}
