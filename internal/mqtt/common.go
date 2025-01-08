package mqtt

import "github.com/RewstApp/agent-smith-go/internal/utils"

type Connection interface {
	MessageChannel() <-chan []byte
	Close()
}

func Subscribe(config utils.Config) (Connection, error) {
	switch config.Broker {
	// TODO: Support other brokers here
	default:
		// Azure IoT Hub is the default
		return SubscribeToAzureIotHub(config)
	}
}
