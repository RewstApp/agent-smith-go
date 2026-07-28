package utils

import (
	"math/rand/v2"
	"time"
)

const maxTimeout time.Duration = 64 * time.Second

// InitialReconnectInterval is the base interval the reconnect backoff seeds
// from. The first reconnect slot produced by ReconnectTimeoutGenerator is
// therefore ~2x this value (doubled) with up to ±25% jitter, i.e. it is never
// shorter than 1.5 * InitialReconnectInterval.
const InitialReconnectInterval time.Duration = time.Second

// DefaultMqttConnectTimeout bounds a single MQTT connect attempt.
//
// This is intentionally decoupled from InitialReconnectInterval. An earlier
// design capped the connect timeout at the shortest reconnect backoff slot
// (1.5 * InitialReconnectInterval, i.e. ~1s) so a connect attempt could never
// outlive the next backoff slot. In practice that cap was far too aggressive:
// the TCP + TLS + MQTT CONNACK handshake to Azure IoT Hub routinely takes
// longer than a second on slow, high-latency, or lossy links (satellite,
// congested VPN, poor cellular, distant regions), so every attempt timed out
// before it could complete and the device never came online.
//
// The connect timeout and the reconnect backoff schedule serve different
// purposes and are now sized independently: the backoff (ReconnectTimeout
// Generator) governs how long to wait *between* attempts, while this value
// governs how long a single attempt is allowed to run. A slow-but-successful
// handshake is now allowed to complete rather than being aborted prematurely;
// the reconnect loop still backs off and retries when an attempt genuinely
// fails. 30s comfortably accommodates a slow TLS handshake while still bounding
// a truly stuck attempt. Endpoints that need even more can raise it per-device
// via the device config (mqtt_connect_timeout_seconds).
const DefaultMqttConnectTimeout time.Duration = 30 * time.Second

// DefaultMqttKeepAlive is the interval at which the client sends MQTT PING
// requests to the broker while otherwise idle. It is configured explicitly
// rather than relying on paho's implicit default so the liveness behavior of a
// marginal connection is predictable. Together with DefaultMqttPingTimeout it
// bounds how quickly a silently-dropped connection is detected: the client
// declares the connection lost (triggering the reconnect loop) within roughly
// DefaultMqttKeepAlive + DefaultMqttPingTimeout.
const DefaultMqttKeepAlive time.Duration = 30 * time.Second

// DefaultMqttPingTimeout is how long the client waits for a PINGRESP after
// sending a PING before considering the connection dead. Set explicitly for the
// same predictability reason as DefaultMqttKeepAlive.
const DefaultMqttPingTimeout time.Duration = 10 * time.Second

// DefaultSasTokenLifetime is the lifetime of the Azure IoT Hub SAS token minted
// for each MQTT connection.
//
// Azure IoT Hub forcibly disconnects a client the moment its SAS token expires.
// An earlier design minted a token valid for only 1 hour, so every healthy
// connection was torn down roughly once per hour — firing OnConnectionLost,
// logging a spurious Error-level "Connection lost", and forcing a full reconnect
// on every agent forever. At scale that also churned the hub as agents
// reconnected on their own hourly cadence.
//
// The lifetime is raised to 24 hours so the forced-disconnect cadence drops from
// hourly to at most daily, and the agent additionally reconnects gracefully a
// safety margin ahead of expiry (see SasTokenRenewMargin) so the token never
// actually expires on a live connection. Deployments that need a different
// cadence can override it per-device via sas_token_lifetime_hours.
const DefaultSasTokenLifetime time.Duration = 24 * time.Hour

// SasTokenRenewMargin returns how long before an Azure IoT Hub SAS token's
// expiry the agent should proactively reconnect with a freshly minted token.
// Because Azure disconnects a client the instant its token expires, reconnecting
// a safety margin ahead of that deadline keeps the connection continuous and
// lets the reconnect be logged as a routine token renewal rather than an
// Error-level "Connection lost".
//
// The margin is 10% of the token lifetime, floored at 1 minute so even a short
// lifetime still renews ahead of expiry (accommodating clock skew and the
// reconnect handshake), and capped at 15 minutes so a long-lived token spends
// almost its entire lifetime in use rather than reconnecting needlessly early.
// For a lifetime shorter than the floor it falls back to half the lifetime so
// the resulting renew-after delay (lifetime - margin) always stays positive.
func SasTokenRenewMargin(lifetime time.Duration) time.Duration {
	const (
		minMargin = 1 * time.Minute
		maxMargin = 15 * time.Minute
	)
	margin := lifetime / 10
	if margin < minMargin {
		margin = minMargin
	}
	if margin > maxMargin {
		margin = maxMargin
	}
	if margin >= lifetime {
		margin = lifetime / 2
	}
	return margin
}

type ReconnectTimeoutGenerator struct {
	base    time.Duration
	timeout time.Duration
}

func (g *ReconnectTimeoutGenerator) Timeout() time.Duration {
	return g.timeout
}

func (g *ReconnectTimeoutGenerator) Next() {
	if g.base == 0 {
		g.base = InitialReconnectInterval
	}

	g.base *= 2
	if g.base > maxTimeout {
		g.base = maxTimeout
	}

	jitter := time.Duration(float64(g.base) * 0.25 * (2*rand.Float64() - 1))
	g.timeout = g.base + jitter
	if g.timeout > maxTimeout {
		g.timeout = maxTimeout
	}
}

func (g *ReconnectTimeoutGenerator) Clear() {
	g.base = 0
	g.timeout = 0
}
