package utils

import (
	"testing"
	"time"
)

func TestReconnectTimeoutGenerator(t *testing.T) {
	g := ReconnectTimeoutGenerator{}

	// Initially timeout should be 0
	if g.Timeout() != 0 {
		t.Errorf("Expected initial timeout to be 0, got %v", g.Timeout())
	}

	// First Next should set it to 2s
	g.Next()
	if g.Timeout() != 2*time.Second {
		t.Errorf("Expected timeout to be 2s, got %v", g.Timeout())
	}

	// Next calls should double the timeout
	expected := 4 * time.Second
	for range 5 {
		g.Next()
		if g.Timeout() != expected {
			t.Errorf("Expected timeout to be %v, got %v", expected, g.Timeout())
		}
		expected *= 2
	}

	// Ensure the timeout caps at the maximum
	for range 10 {
		g.Next()
	}
	if g.Timeout() != maxTimeout {
		t.Errorf("Expected timeout to cap at 64s, got %v", g.Timeout())
	}

	// Clear should reset the timeout
	g.Clear()
	if g.Timeout() != 0 {
		t.Errorf("Expected timeout to reset to 0 after Clear(), got %v", g.Timeout())
	}
}
