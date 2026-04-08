package utils

import (
	"math/rand/v2"
	"time"
)

const maxTimeout time.Duration = 64 * time.Second

type ReconnectTimeoutGenerator struct {
	base    time.Duration
	timeout time.Duration
}

func (g *ReconnectTimeoutGenerator) Timeout() time.Duration {
	return g.timeout
}

func (g *ReconnectTimeoutGenerator) Next() {
	if g.base == 0 {
		g.base = time.Second
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
