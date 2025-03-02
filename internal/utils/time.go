package utils

import (
	"time"
)

type ReconnectTimeoutGenerator struct {
	timeout time.Duration
}

func (g *ReconnectTimeoutGenerator) Timeout() time.Duration {
	return g.timeout
}

func (g *ReconnectTimeoutGenerator) Next() {
	if g.timeout == 0 {
		g.timeout = time.Second
	}

	g.timeout *= 2

	max := 64 * time.Second
	if g.timeout > max {
		g.timeout = max
	}
}

func (g *ReconnectTimeoutGenerator) Clear() {
	g.timeout = 0
}
