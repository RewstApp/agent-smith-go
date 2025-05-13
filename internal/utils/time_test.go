package utils

import (
	"testing"
	"time"
)

func TestReconnectTimeoutGenerator(t *testing.T) {
	g := ReconnectTimeoutGenerator{}

	if g.Timeout() != 0 {
		t.Errorf("expected initial timeout to be 0, got %v", g.Timeout())
	}

	g.Next()
	if g.Timeout() != 2*time.Second {
		t.Errorf("expected timeout to be 2s, got %v", g.Timeout())
	}

	expected := 4 * time.Second
	for range 5 {
		g.Next()
		if g.Timeout() != expected {
			t.Errorf("expected timeout to be %v, got %v", expected, g.Timeout())
		}
		expected *= 2
	}

	for range 10 {
		g.Next()
	}
	if g.Timeout() != maxTimeout {
		t.Errorf("expected timeout to cap at 64s, got %v", g.Timeout())
	}

	g.Clear()
	if g.Timeout() != 0 {
		t.Errorf("expected timeout to reset to 0 after Clear(), got %v", g.Timeout())
	}
}
