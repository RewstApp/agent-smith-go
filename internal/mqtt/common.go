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
	payload, err := json.Marshal(props)
	if err != nil {
		return fmt.Errorf("failed to marshal reported properties: %w", err)
	}

	if timeout <= 0 {
		timeout = utils.MqttPublishTimeout
	}

	topic := "$iothub/twin/PATCH/properties/reported/?$rid=1"
	token := client.Publish(topic, 0, false, payload)
	if !token.WaitTimeout(timeout) {
		return fmt.Errorf("timed out after %v waiting to publish reported properties", timeout)
	}
	if token.Error() != nil {
		return fmt.Errorf("failed to publish reported properties: %w", token.Error())
	}

	return nil
}
