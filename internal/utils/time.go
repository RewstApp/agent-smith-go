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
