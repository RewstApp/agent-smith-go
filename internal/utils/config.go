package utils

import (
	"encoding/json"
	"io"
	"os"
)

var ConfigFileName = "config.json"

type Config struct {
	DeviceId        string `json:"device_id"`
	RewstOrgId      string `json:"rewst_org_id"`
	RewstEngineHost string `json:"rewst_engine_host"`
	SharedAccessKey string `json:"shared_access_key"`
	AzureIotHubHost string `json:"azure_iot_hub_host"`
}

func (c Config) ConnectionString() string {
	return "HostName=" + c.AzureIotHubHost + ";DeviceId=" + c.DeviceId + ";SharedAccessKey=" + c.SharedAccessKey
}

func (c *Config) Load(configFilePath string) error {

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
	err = json.Unmarshal(data, c)
	if err != nil {
		return err
	}

	// No error
	return nil
}
