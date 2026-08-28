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
	// MqttSubscribeTimeoutSeconds optionally overrides how long the agent waits
	// for the broker to acknowledge its SUBSCRIBE before treating the connection
	// attempt as failed. When unset (or non-positive) the agent falls back to
	// utils.DefaultMqttSubscribeTimeout. Raising it suits an endpoint whose
	// broker legitimately acknowledges slowly; lowering it makes a throttling or
	// half-open broker be abandoned (and retried on the reconnect backoff) sooner.
	MqttSubscribeTimeoutSeconds *int `json:"mqtt_subscribe_timeout_seconds,omitempty"`
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
	// CommandTimeoutSeconds optionally overrides how long a single received
	// command is allowed to run before it is killed. When unset (or
	// non-positive) the agent falls back to DefaultCommandTimeout rather than
	// running commands unbounded, so a hung or interactive script (infinite
	// loop, blocked on stdin, stuck network call) can no longer permanently
	// consume a worker out of the box: the command is cancelled once the
	// deadline elapses even if the MQTT connection stays up. Raise it for
	// workflows with legitimately long-running commands, or lower it to fail
	// faster.
	CommandTimeoutSeconds *int `json:"command_timeout_seconds,omitempty"`
	// MaxOutputBytes optionally overrides how many bytes of a single command's
	// output the agent keeps, applied independently to stdout and stderr. When
	// unset (or non-positive) the agent falls back to DefaultMaxOutputBytes.
	// Output past the ceiling is discarded instead of buffered, so a script that
	// writes an unbounded volume (a full filesystem listing, a large event-log
	// dump, an error loop printing per iteration) can no longer exhaust memory
	// and get the agent OOM-killed; the result posted back is flagged as
	// truncated and carries both byte counts. Raise it for a device that
	// legitimately returns very large results, lower it to tighten the memory
	// ceiling.
	MaxOutputBytes *int `json:"max_output_bytes,omitempty"`
	// SasTokenLifetimeHours optionally overrides the lifetime of the Azure IoT
	// Hub SAS token minted for each MQTT connection, in hours. When unset (or
	// non-positive) the agent falls back to utils.DefaultSasTokenLifetime. Azure
	// IoT Hub disconnects a client when its SAS token expires; the agent
	// proactively reconnects with a fresh token a safety margin ahead of that
	// deadline (see utils.SasTokenRenewMargin), so a longer lifetime means less
	// frequent — but always graceful — reconnects.
	SasTokenLifetimeHours *int `json:"sas_token_lifetime_hours,omitempty"`
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
	// DefaultPostbackMaxRetryBackoff caps the exponential-backoff delay between
	// postback attempts regardless of how high PostbackMaxAttempts is raised. The
	// in-line schedule doubles the base delay on every retry (base * 2^n); without
	// a ceiling a moderately large postback_max_attempts produces multi-hour or
	// multi-day sleeps that pin a worker, and a large value overflows the shift
	// into a negative time.Duration that makes time.After fire immediately and the
	// retry loop busy-spin. This mirrors maxTimeout in the reconnect backoff
	// generator (see internal/utils/time.go): the per-slot wait is bounded so the
	// total retry window still widens with more attempts, but no single sleep can
	// overflow, hang a worker for days, or collapse into a tight loop.
	DefaultPostbackMaxRetryBackoff = 64 * time.Second
	// DefaultMaxOutputBytes is the ceiling on how much of a single command's
	// output the agent keeps, applied independently to stdout and stderr, used
	// when MaxOutputBytes is not configured. 10 MiB per stream is far above any
	// legitimate observed command result, so existing workflows see no
	// behavioral change, while bounding the memory one command's output can cost
	// the agent to a small constant multiple of it instead of tracking however
	// much the script decides to write.
	DefaultMaxOutputBytes = 10 * 1024 * 1024
	// DefaultCommandTimeout bounds how long a single received command may run
	// when CommandTimeoutSeconds is not configured. Without a default, a hung
	// command (infinite loop, blocked on stdin, stuck network call) occupies its
	// worker forever, and a small fixed worker pool with back-pressure (rather
	// than dropping messages) means a handful of hangs — or one buggy workflow
	// that repeats the same hanging command — can silently stall all command
	// execution on the device. 30 minutes is generous enough to not affect
	// legitimate long-running commands (installers, large file operations)
	// while still guaranteeing every worker is eventually reclaimed.
	DefaultCommandTimeout = 30 * time.Minute
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

// ResolvedCommandTimeout returns the per-command execution timeout, honoring
// the per-device override when set to a positive value and falling back to
// DefaultCommandTimeout otherwise. It is always positive, so command execution
// is never unbounded by default.
func (d Device) ResolvedCommandTimeout() time.Duration {
	if d.CommandTimeoutSeconds != nil && *d.CommandTimeoutSeconds > 0 {
		return time.Duration(*d.CommandTimeoutSeconds) * time.Second
	}
	return DefaultCommandTimeout
}

// ResolvedMaxOutputBytes returns the per-stream ceiling on captured command
// output, honoring the per-device override when set to a positive value and
// falling back to DefaultMaxOutputBytes otherwise. It is always positive, so the
// bound can never be disabled (or collapsed to zero) by configuration.
func (d Device) ResolvedMaxOutputBytes() int {
	if d.MaxOutputBytes != nil && *d.MaxOutputBytes > 0 {
		return *d.MaxOutputBytes
	}
	return DefaultMaxOutputBytes
}

// MqttConnectTimeout returns the per-attempt MQTT connect timeout, honoring the
// per-device override when set and falling back to the documented default.
func (d Device) MqttConnectTimeout() time.Duration {
	if d.MqttConnectTimeoutSeconds != nil && *d.MqttConnectTimeoutSeconds > 0 {
		return time.Duration(*d.MqttConnectTimeoutSeconds) * time.Second
	}
	return utils.DefaultMqttConnectTimeout
}

// MqttSubscribeTimeout returns how long to wait for the broker's SUBACK before
// treating the connection attempt as failed, honoring the per-device override
// when set to a positive value and falling back to the documented default. It is
// always positive, so the bound can never be disabled by configuration.
func (d Device) MqttSubscribeTimeout() time.Duration {
	if d.MqttSubscribeTimeoutSeconds != nil && *d.MqttSubscribeTimeoutSeconds > 0 {
		return time.Duration(*d.MqttSubscribeTimeoutSeconds) * time.Second
	}
	return utils.DefaultMqttSubscribeTimeout
}

// sasTokenLifetimeOverrideStr is overridable via -ldflags for integration
// testing. When set to a valid, positive Go duration it forces the SAS token
// lifetime — and therefore the proactive-renewal cadence (see
// utils.SasTokenRenewMargin) — to a short, seconds-scale value so the renewal
// path can be exercised in seconds instead of the production hours. It is empty
// in production builds and, when set, takes precedence over the config-driven
// sas_token_lifetime_hours.
// Example: -ldflags "-X github.com/RewstApp/agent-smith-go/internal/agent.sasTokenLifetimeOverrideStr=90s"
var sasTokenLifetimeOverrideStr = ""

// SasTokenLifetime returns the lifetime of the Azure IoT Hub SAS token minted
// for each MQTT connection. An ldflags-injected override (integration builds
// only) wins when set; otherwise it honors the per-device
// sas_token_lifetime_hours when set to a positive value and falls back to
// utils.DefaultSasTokenLifetime.
func (d Device) SasTokenLifetime() time.Duration {
	if sasTokenLifetimeOverrideStr != "" {
		if lifetime, err := time.ParseDuration(sasTokenLifetimeOverrideStr); err == nil &&
			lifetime > 0 {
			return lifetime
		}
	}
	if d.SasTokenLifetimeHours != nil && *d.SasTokenLifetimeHours > 0 {
		return time.Duration(*d.SasTokenLifetimeHours) * time.Hour
	}
	return utils.DefaultSasTokenLifetime
}

type Plugin struct {
	Name           string `json:"name"`
	ExecutablePath string `json:"executable_path"`
}
