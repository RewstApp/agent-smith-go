package utils

import (
	"context"
	"time"
)

type ReconnectTimeoutGenerator struct {
	timeout time.Duration
}

func (g *ReconnectTimeoutGenerator) Next() time.Duration {
	if g.timeout == 0 {
		g.timeout = time.Duration(2) * time.Second
	}

	g.timeout *= 2

	max := time.Duration(64) * time.Second
	if g.timeout > max {
		g.timeout = max
	}

	return g.timeout
}

func (g *ReconnectTimeoutGenerator) Clear() {
	g.timeout = 0
}

func CancelableSleep(ctx context.Context, duration time.Duration) error {
	select {
	case <-time.After(duration):
		// Sleep completed
		return nil
	case <-ctx.Done():
		// Context canceled
		return ctx.Err()
	}
}
