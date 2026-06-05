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

// TestDefaultMqttConnectTimeoutFitsBackoff records the chosen connect timeout
// and its rationale: a single connect attempt must never outlive the shortest
// reconnect backoff slot, otherwise a connect attempt could still be blocking
// when the next reconnect would already be due. The shortest slot is the first
// one: base doubled to 2 * InitialReconnectInterval, minus up to 25% jitter,
// i.e. 1.5 * InitialReconnectInterval.
func TestDefaultMqttConnectTimeoutFitsBackoff(t *testing.T) {
	shortestSlot := time.Duration(float64(2*InitialReconnectInterval) * (1 - 0.25))
	if DefaultMqttConnectTimeout > shortestSlot {
		t.Errorf(
			"DefaultMqttConnectTimeout (%v) must not exceed the shortest backoff slot (%v)",
			DefaultMqttConnectTimeout,
			shortestSlot,
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
