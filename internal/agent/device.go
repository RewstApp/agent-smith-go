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
	// WorkerCount optionally overrides how many concurrent command-execution
	// workers drain the inbound message queue. When unset (or non-positive) the
	// agent falls back to DefaultWorkerCount. Deployments that expect a high
	// volume of concurrent commands can raise this to widen execution
	// parallelism.
	WorkerCount *int `json:"worker_count,omitempty"`
	// MessageQueueSize optionally overrides the capacity of the buffered queue
	// that holds received messages waiting for a worker. When unset (or
	// non-positive) the agent falls back to DefaultMessageQueueSize. A larger
	// queue absorbs bigger bursts before the agent starts applying back-pressure
	// to the broker.
	MessageQueueSize *int `json:"message_queue_size,omitempty"`
	// PostbackMaxAttempts optionally overrides the total number of postback
	// attempts (including the initial try) before a command result is spooled to
	// disk for later delivery. When unset (or non-positive) the agent falls back
	// to DefaultPostbackMaxAttempts. Raising it widens the in-line retry window
	// for transient engine outages.
	PostbackMaxAttempts *int `json:"postback_max_attempts,omitempty"`
	// PostbackBaseRetryBackoffSeconds optionally overrides the base delay used for
	// exponential backoff between postback attempts, in seconds. When unset (or
	// non-positive) the agent falls back to DefaultPostbackBaseRetryBackoff.
	PostbackBaseRetryBackoffSeconds *int `json:"postback_base_retry_backoff_seconds,omitempty"`
	// CommandTimeoutSeconds optionally bounds how long a single received command
	// is allowed to run before it is killed. When unset (or non-positive) command
	// execution is unbounded, preserving the historical behavior. Setting a
	// positive value protects the worker pool from a hung or interactive script
	// (infinite loop, blocked on stdin, stuck network call) permanently consuming
	// a worker: the command is cancelled once the deadline elapses even if the
	// MQTT connection stays up.
	CommandTimeoutSeconds *int `json:"command_timeout_seconds,omitempty"`
}

const (
	// DefaultWorkerCount is the number of concurrent command-execution workers
	// used when WorkerCount is not configured.
	DefaultWorkerCount = 10
	// DefaultMessageQueueSize is the buffered inbound message queue capacity used
	// when MessageQueueSize is not configured.
	DefaultMessageQueueSize = 100
	// DefaultPostbackMaxAttempts is the total number of postback attempts used
	// when PostbackMaxAttempts is not configured.
	DefaultPostbackMaxAttempts = 3
	// DefaultPostbackBaseRetryBackoff is the base exponential-backoff delay used
	// between postback attempts when PostbackBaseRetryBackoffSeconds is not
	// configured.
	DefaultPostbackBaseRetryBackoff = 1 * time.Second
)

// ResolvedWorkerCount returns the number of command-execution workers to start,
// honoring the per-device override when set to a positive value and falling back
// to DefaultWorkerCount otherwise.
func (d Device) ResolvedWorkerCount() int {
	if d.WorkerCount != nil && *d.WorkerCount > 0 {
		return *d.WorkerCount
	}
	return DefaultWorkerCount
}

// ResolvedMessageQueueSize returns the inbound message queue capacity, honoring
// the per-device override when set to a positive value and falling back to
// DefaultMessageQueueSize otherwise.
func (d Device) ResolvedMessageQueueSize() int {
	if d.MessageQueueSize != nil && *d.MessageQueueSize > 0 {
		return *d.MessageQueueSize
	}
	return DefaultMessageQueueSize
}

// ResolvedPostbackMaxAttempts returns the total number of postback attempts,
// honoring the per-device override when set to a positive value and falling back
// to DefaultPostbackMaxAttempts otherwise.
func (d Device) ResolvedPostbackMaxAttempts() int {
	if d.PostbackMaxAttempts != nil && *d.PostbackMaxAttempts > 0 {
		return *d.PostbackMaxAttempts
	}
	return DefaultPostbackMaxAttempts
}

// ResolvedPostbackBaseRetryBackoff returns the base exponential-backoff delay
// between postback attempts, honoring the per-device override when set to a
// positive value and falling back to DefaultPostbackBaseRetryBackoff otherwise.
func (d Device) ResolvedPostbackBaseRetryBackoff() time.Duration {
	if d.PostbackBaseRetryBackoffSeconds != nil && *d.PostbackBaseRetryBackoffSeconds > 0 {
		return time.Duration(*d.PostbackBaseRetryBackoffSeconds) * time.Second
	}
	return DefaultPostbackBaseRetryBackoff
}

// ResolvedCommandTimeout returns the per-command execution timeout and whether
// one is configured. It reports ok=false when CommandTimeoutSeconds is unset or
// non-positive, in which case command execution is unbounded (historical
// behavior); otherwise it returns the configured duration with ok=true.
func (d Device) ResolvedCommandTimeout() (time.Duration, bool) {
	if d.CommandTimeoutSeconds != nil && *d.CommandTimeoutSeconds > 0 {
		return time.Duration(*d.CommandTimeoutSeconds) * time.Second, true
	}
	return 0, false
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
