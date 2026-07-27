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

// TestSasTokenRenewMargin asserts the renew margin is always a positive
// duration strictly less than the token lifetime, so the derived renew-after
// delay (lifetime - margin) is positive and the agent reconnects ahead of
// expiry across the full range of configurable lifetimes.
func TestSasTokenRenewMargin(t *testing.T) {
	tests := []struct {
		name     string
		lifetime time.Duration
		expect   time.Duration
	}{
		// 10% of lifetime when that sits between the floor and cap.
		{"one hour uses 10 percent", time.Hour, 6 * time.Minute},
		// 10% of 24h is 2.4h, capped at 15m.
		{"one day capped at max", 24 * time.Hour, 15 * time.Minute},
		// 10% of 5m is 30s, floored at 1m.
		{"short lifetime floored", 5 * time.Minute, time.Minute},
		// Lifetime below the floor falls back to half the lifetime so
		// renew-after stays positive.
		{"tiny lifetime uses half", 30 * time.Second, 15 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SasTokenRenewMargin(tt.lifetime)
			if got != tt.expect {
				t.Errorf("SasTokenRenewMargin(%v) = %v, want %v", tt.lifetime, got, tt.expect)
			}
			if got <= 0 {
				t.Errorf("SasTokenRenewMargin(%v) must be positive, got %v", tt.lifetime, got)
			}
			if got >= tt.lifetime {
				t.Errorf(
					"SasTokenRenewMargin(%v) = %v must be < lifetime so renew-after stays positive",
					tt.lifetime,
					got,
				)
			}
		})
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
