package mqtt

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type (
	Client  = mqtt.Client
	Message = mqtt.Message
	Token   = mqtt.Token
)

var NewClient = mqtt.NewClient

const DefaultDisconnectQuiesce time.Duration = 250 * time.Millisecond

// TokenWaitResult reports how a bounded wait on an MQTT token ended.
type TokenWaitResult int

const (
	// TokenCompleted means the broker answered (or the operation otherwise
	// resolved) before the deadline. The caller must still inspect Token.Error.
	TokenCompleted TokenWaitResult = iota
	// TokenTimedOut means the deadline elapsed with the token unresolved.
	TokenTimedOut
	// TokenInterrupted means the caller's interrupt channel fired first.
	TokenInterrupted
)

// WaitToken waits for an MQTT token to resolve, bounded by timeout and
// interruptible by the interrupt channel.
//
// paho's Token.Wait blocks until the broker responds or the TCP connection
// breaks, with no deadline and no way to abort. A broker that keeps the socket
// open and answers PINGREQ but stops responding to control packets — a
// throttling Azure IoT Hub, or a middlebox that half-opens the connection —
// therefore parks the caller indefinitely. Selecting on Token.Done alongside a
// timer and the caller's interrupt channel gives both a hard ceiling and prompt
// cancellation, so a wedged broker can never outlive the timeout and a service
// stop is never queued behind one.
//
// interrupt may be nil, in which case the wait is bounded but not interruptible.
//
// An already-resolved token deterministically wins over an already-pending
// interrupt: it is checked on its own first, before the blocking select. Leaving
// both to the same select would let Go pick a ready case at random, so a
// connection cycle racing a stop would abandon a completed operation only some
// of the time. Preferring the resolved token costs nothing — there is nothing to
// wait for, so the stop is not delayed, and the caller honors it at its next
// wait or at its select on the stop signal.
func WaitToken(token Token, timeout time.Duration, interrupt <-chan struct{}) TokenWaitResult {
	done := token.Done()

	select {
	case <-done:
		return TokenCompleted
	default:
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
		return TokenCompleted
	case <-interrupt:
		return TokenInterrupted
	case <-timer.C:
		return TokenTimedOut
	}
}

func NewClientOptions(device agent.Device) (*mqtt.ClientOptions, error) {
	var (
		opts *mqtt.ClientOptions
		err  error
	)

	switch device.Broker {
	default:
		opts, err = newAzureIotHubClientOptions(azureIotHubDevice{
			DeviceId:        device.DeviceId,
			Host:            device.AzureIotHubHost,
			SharedAccessKey: device.SharedAccessKey,
			TokenLifetime:   device.SasTokenLifetime(),
		})
	}

	if err != nil {
		return nil, err
	}

	// Explicitly own the connect timeout instead of relying on paho's implicit
	// 30s default, so reconnect timing stays predictable across paho upgrades.
	// See utils.DefaultMqttConnectTimeout for the value and rationale.
	opts.SetConnectTimeout(device.MqttConnectTimeout())

	// Explicitly configure keepalive/ping behavior rather than relying on paho's
	// implicit defaults, so a marginal connection is detected and the reconnect
	// loop is triggered within a bounded, predictable time. See
	// utils.DefaultMqttKeepAlive / utils.DefaultMqttPingTimeout for the rationale.
	opts.SetKeepAlive(utils.DefaultMqttKeepAlive)
	opts.SetPingTimeout(utils.DefaultMqttPingTimeout)

	return opts, nil
}

type ReportedProperties struct {
	AgentVersion string `json:"agent_version"`
	// Health is the agent's own view of its state (sc-129687): the command
	// backlog and the journal's condition, the postback spool and what it has
	// dropped, the notification plugins' supervision counters, the command
	// outcomes and the connection. Every counter is since process start. nil
	// omits the object, which is what the first publish of an older agent sent.
	Health *Health `json:"health,omitempty"`
	// Commands carries the running commands' progress heartbeats (sc-129689).
	// The service sends it on every publish, empty when nothing runs, so the
	// list a previous process left behind is always overwritten; nil (an older
	// agent, or a health file written by one) omits the object.
	Commands *CommandsProgress `json:"commands,omitempty"`
	// SettingsAck acknowledges the desired properties the agent was sent, one
	// entry per key (sc-129688). IoT Hub merges reported properties key by
	// key, so a PATCH carrying some keys leaves the others' acknowledgements
	// in place; nil omits the object.
	SettingsAck map[string]SettingAck `json:"settings_ack,omitempty"`
}

// CommandsProgress is the device twin's running-commands object.
type CommandsProgress struct {
	InFlight []CommandProgress `json:"in_flight"`
}

// CommandProgress is one running command's heartbeat: the output counts are
// bytes produced, which keep growing past the output ceiling.
type CommandProgress struct {
	PostID         string `json:"post_id"`
	StartedAt      string `json:"started_at"`
	ElapsedSeconds int64  `json:"elapsed_seconds"`
	StdoutBytes    int64  `json:"stdout_bytes"`
	StderrBytes    int64  `json:"stderr_bytes"`
	LastOutputLine string `json:"last_output_line"`
}

// Health is the device twin's fleet-health object. The JSON shape is a
// documented contract (README "Fleet Health in the Device Twin"); a field is
// added, never renamed.
type Health struct {
	ReportedAt          string         `json:"reported_at"`
	UptimeSeconds       int64          `json:"uptime_seconds"`
	ConnectedAt         string         `json:"connected_at,omitempty"`
	LastConnectionError string         `json:"last_connection_error"`
	Journal             JournalHealth  `json:"journal"`
	Spool               SpoolHealth    `json:"spool"`
	Plugins             []PluginHealth `json:"plugins"`
	Commands            CommandHealth  `json:"commands"`
}

type JournalHealth struct {
	Pending          int   `json:"pending"`
	Degraded         bool  `json:"degraded"`
	ExpiredTotal     int64 `json:"expired_total"`
	InterruptedTotal int64 `json:"interrupted_total"`
}

type SpoolHealth struct {
	Entries         int   `json:"entries"`
	DroppedExpired  int64 `json:"dropped_expired"`
	DroppedCapacity int64 `json:"dropped_capacity"`
	DroppedAttempts int64 `json:"dropped_attempts"`
	DroppedCorrupt  int64 `json:"dropped_corrupt"`
}

type PluginHealth struct {
	Name           string `json:"name"`
	Restarts       int64  `json:"restarts"`
	NotifyFailures int64  `json:"notify_failures"`
	NotifyTimeouts int64  `json:"notify_timeouts"`
}

type CommandHealth struct {
	Executing      int32 `json:"executing"`
	CompletedTotal int64 `json:"completed_total"`
	TimedOutTotal  int64 `json:"timed_out_total"`
	// FailedTotal counts commands that could not be run at all (script not
	// written or verified, shell not started); a command interrupted by a
	// service stop is counted by none of these - the journal replay reports
	// it as interrupted.
	FailedTotal int64 `json:"failed_total"`
}

// Same reports whether two health objects describe the same state once the
// fields that change on every tick (the report time and the uptime) are set
// aside, so the reporter publishes on change and not on the clock.
func (h *Health) Same(other *Health) bool {
	if h == nil || other == nil {
		return h == other
	}
	if h.ConnectedAt != other.ConnectedAt ||
		h.LastConnectionError != other.LastConnectionError ||
		h.Journal != other.Journal ||
		h.Spool != other.Spool ||
		h.Commands != other.Commands ||
		len(h.Plugins) != len(other.Plugins) {
		return false
	}
	for i := range h.Plugins {
		if h.Plugins[i] != other.Plugins[i] {
			return false
		}
	}
	return true
}

// UpdateReportedProperties publishes reported properties to the Azure IoT Hub
// device twin, waiting at most timeout for the publish to resolve.
//
// The wait is bounded because this publish sits between connect and subscribe on
// the connection path: a broker that accepts the connection but stops answering
// control packets would otherwise wedge the cycle here, before the agent has
// subscribed to anything. A timeout is reported as an error like any other
// publish failure; the caller already treats a failed twin update as non-fatal.
// A non-positive timeout falls back to utils.MqttPublishTimeout so the wait can
// never be unbounded.
func UpdateReportedProperties(
	client mqtt.Client,
	props ReportedProperties,
	timeout time.Duration,
) error {
	return UpdateReportedPropertiesRID(client, props, "1", timeout)
}

// UpdateReportedPropertiesRID is UpdateReportedProperties under the request id
// rid, so the hub's answer on the twin response topic can be matched to this
// publish (sc-129688).
func UpdateReportedPropertiesRID(
	client mqtt.Client,
	props ReportedProperties,
	rid string,
	timeout time.Duration,
) error {
	payload, err := json.Marshal(props)
	if err != nil {
		return fmt.Errorf("failed to marshal reported properties: %w", err)
	}

	if timeout <= 0 {
		timeout = utils.MqttPublishTimeout
	}

	topic := "$iothub/twin/PATCH/properties/reported/?$rid=" + rid
	token := client.Publish(topic, 0, false, payload)
	if !token.WaitTimeout(timeout) {
		return fmt.Errorf("timed out after %v waiting to publish reported properties", timeout)
	}
	if token.Error() != nil {
		return fmt.Errorf("failed to publish reported properties: %w", token.Error())
	}

	return nil
}
