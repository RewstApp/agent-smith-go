package agent

import "github.com/RewstApp/agent-smith-go/internal/utils"

type Device struct {
	DeviceId        string             `json:"device_id"`
	RewstOrgId      string             `json:"rewst_org_id"`
	RewstEngineHost string             `json:"rewst_engine_host"`
	SharedAccessKey string             `json:"shared_access_key"`
	AzureIotHubHost string             `json:"azure_iot_hub_host"`
	Broker          string             `json:"broker"`
	LoggingLevel    utils.LoggingLevel `json:"logging_level"`
	UseSyslog       bool               `json:"syslog"`
}
