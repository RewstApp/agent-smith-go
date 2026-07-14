package utils

import (
	"testing"
	"time"
)

func assertInJitterRange(t *testing.T, got, base time.Duration, jitterFraction float64) {
	t.Helper()
	low := time.Duration(float64(base) * (1 - jitterFraction))
	high := time.Duration(float64(base) * (1 + jitterFraction))
	if got < low || got > high {
		t.Errorf("expected timeout in [%v, %v], got %v", low, high, got)
	}
}

func TestReconnectTimeoutGenerator(t *testing.T) {
	g := ReconnectTimeoutGenerator{}

	if g.Timeout() != 0 {
		t.Errorf("expected initial timeout to be 0, got %v", g.Timeout())
	}

	// First Next(): base = 2s, jitter ±25% → [1.5s, 2.5s]
	g.Next()
	assertInJitterRange(t, g.Timeout(), 2*time.Second, 0.25)

	// Subsequent calls double the base; assert jittered value is within ±25% of that base
	for _, base := range []time.Duration{4, 8, 16, 32, 64} {
		g.Next()
		assertInJitterRange(t, g.Timeout(), base*time.Second, 0.25)
	}

	// Cap is respected after jitter
	for range 10 {
		g.Next()
		if g.Timeout() > maxTimeout {
			t.Errorf("expected timeout to be capped at %v, got %v", maxTimeout, g.Timeout())
		}
	}

	g.Clear()
	if g.Timeout() != 0 {
		t.Errorf("expected timeout to reset to 0 after Clear(), got %v", g.Timeout())
	}
}

// TestDefaultMqttConnectTimeoutIsDecoupledFromBackoff records the intentionally
// decoupled relationship between the connect timeout and the reconnect backoff
// schedule.
//
// An earlier design capped DefaultMqttConnectTimeout at the shortest reconnect
// backoff slot (1.5 * InitialReconnectInterval, ~1s) so a connect attempt could
// never outlive the next backoff slot. That cap made the agent unable to
// connect on slow/high-latency links where the TLS handshake alone exceeds a
// second. The two values now serve independent purposes — the backoff governs
// the wait *between* attempts, the connect timeout governs how long a single
// attempt may run — so the connect timeout is sized to accommodate a slow
// handshake and is deliberately larger than the shortest backoff slot. This
// test asserts that decoupling holds (and would fail if the old cap were
// reintroduced), and pins the default to a value comfortably above a
// slow-handshake threshold.
func TestDefaultMqttConnectTimeoutIsDecoupledFromBackoff(t *testing.T) {
	shortestSlot := time.Duration(float64(2*InitialReconnectInterval) * (1 - 0.25))
	if DefaultMqttConnectTimeout <= shortestSlot {
		t.Errorf(
			"DefaultMqttConnectTimeout (%v) is expected to be decoupled from and larger "+
				"than the shortest backoff slot (%v) so slow handshakes can complete",
			DefaultMqttConnectTimeout,
			shortestSlot,
		)
	}

	// Guard against a regression that silently lowers the default back toward
	// the old ~1s cap: it must comfortably accommodate a slow TLS handshake.
	const minAcceptable = 10 * time.Second
	if DefaultMqttConnectTimeout < minAcceptable {
		t.Errorf(
			"DefaultMqttConnectTimeout (%v) must be at least %v to accommodate slow handshakes",
			DefaultMqttConnectTimeout,
			minAcceptable,
		)
	}
}

func TestReconnectTimeoutGeneratorJitterDiffers(t *testing.T) {
	// Two independent generators must produce different sequences
	g1 := ReconnectTimeoutGenerator{}
	g2 := ReconnectTimeoutGenerator{}

	different := false
	for range 20 {
		g1.Next()
		g2.Next()
		if g1.Timeout() != g2.Timeout() {
			different = true
			break
		}
	}
	if !different {
		t.Error("expected two independent generators to produce different jittered sequences")
	}
}
