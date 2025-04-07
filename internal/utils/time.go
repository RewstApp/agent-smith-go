package utils

import (
	"time"
)

const maxTimeout time.Duration = 64 * time.Second

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

	if g.timeout > maxTimeout {
		g.timeout = maxTimeout
	}
}

func (g *ReconnectTimeoutGenerator) Clear() {
	g.timeout = 0
}
