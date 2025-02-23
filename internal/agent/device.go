package agent

import (
	"context"
	"encoding/json"
	"io"
	"os"

	"github.com/RewstApp/agent-smith-go/internal/mqtt"
)

type Device struct {
	DeviceId        string `json:"device_id"`
	RewstOrgId      string `json:"rewst_org_id"`
	RewstEngineHost string `json:"rewst_engine_host"`
	SharedAccessKey string `json:"shared_access_key"`
	AzureIotHubHost string `json:"azure_iot_hub_host"`
	Broker          string `json:"broker"`
}

func (device *Device) Load(configFilePath string) error {

	// Open the JSON file
	file, err := os.Open(configFilePath)
	if err != nil {
		return err
	}
	defer file.Close()

	// Read the file contents
	data, err := io.ReadAll(file)
	if err != nil {
		return err
	}

	// Parse the JSON data
	err = json.Unmarshal(data, device)
	if err != nil {
		return err
	}

	// No error
	return nil
}

func (device *Device) Subscribe(ctx context.Context) <-chan mqtt.Event {
	switch device.Broker {
	// TODO: Support other brokers here
	default:
		// Azure IoT Hub is the default
		return mqtt.SubscribeToAzureIotHub(ctx, &mqtt.AzureIotHubDevice{
			DeviceId:        device.DeviceId,
			Host:            device.AzureIotHubHost,
			SharedAccessKey: device.SharedAccessKey,
		})
	}
}
