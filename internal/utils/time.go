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
// paho.mqtt.golang defaults ConnectTimeout to an implicit 30s. We set our own
// value so reconnect timing is fully owned by this codebase and stays stable
// across paho upgrades. The value is intentionally <= the shortest reconnect
// backoff slot (1.5 * InitialReconnectInterval) so a connect attempt can never
// outlive the next backoff slot. Endpoints with slow TLS handshakes can raise
// it per-device via the device config (mqtt_connect_timeout_seconds).
const DefaultMqttConnectTimeout time.Duration = InitialReconnectInterval

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
