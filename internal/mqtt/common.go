package mqtt

import (
	"github.com/RewstApp/agent-smith-go/internal/utils"
)

type Message struct {
	Payload []byte
	Error   error
}

func Subscribe(config utils.Config, stop <-chan struct{}) <-chan Message {
	switch config.Broker {
	// TODO: Support other brokers here
	default:
		// Azure IoT Hub is the default
		return subscribeToAzureIotHub(config, stop)
	}
}
