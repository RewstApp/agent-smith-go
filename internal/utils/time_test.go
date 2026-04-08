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
