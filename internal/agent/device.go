package agent

import (
	"time"

	"github.com/RewstApp/agent-smith-go/internal/utils"
)

type Device struct {
	DeviceId             string             `json:"device_id"`
	RewstOrgId           string             `json:"rewst_org_id"`
	RewstEngineHost      string             `json:"rewst_engine_host"`
	SharedAccessKey      string             `json:"shared_access_key"`
	AzureIotHubHost      string             `json:"azure_iot_hub_host"`
	Broker               string             `json:"broker"`
	LoggingLevel         utils.LoggingLevel `json:"logging_level"`
	UseSyslog            bool               `json:"syslog"`
	Plugins              []Plugin           `json:"plugins"`
	DisableAgentPostback bool               `json:"disable_agent_postback"`
	DisableAutoUpdates   bool               `json:"disable_auto_updates"`
	GithubToken          string             `json:"github_token,omitempty"`
	MqttQos              *byte              `json:"mqtt_qos,omitempty"`
	// MqttConnectTimeoutSeconds optionally overrides the per-attempt MQTT
	// connect timeout. When unset (or non-positive) the agent falls back to
	// utils.DefaultMqttConnectTimeout. Useful for endpoints with slow TLS
	// handshakes that need more than the default.
	MqttConnectTimeoutSeconds *int `json:"mqtt_connect_timeout_seconds,omitempty"`
}

// MqttConnectTimeout returns the per-attempt MQTT connect timeout, honoring the
// per-device override when set and falling back to the documented default.
func (d Device) MqttConnectTimeout() time.Duration {
	if d.MqttConnectTimeoutSeconds != nil && *d.MqttConnectTimeoutSeconds > 0 {
		return time.Duration(*d.MqttConnectTimeoutSeconds) * time.Second
	}
	return utils.DefaultMqttConnectTimeout
}

type Plugin struct {
	Name           string `json:"name"`
	ExecutablePath string `json:"executable_path"`
}
