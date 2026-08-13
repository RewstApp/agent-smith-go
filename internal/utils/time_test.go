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

// TestJitteredBackoffSchedule verifies the exponential schedule doubles per step
// and flattens at the cap. Jitter is bounded to ±25%, so each slot is asserted
// within that band rather than exactly.
func TestJitteredBackoffSchedule(t *testing.T) {
	base := time.Second
	maxBackoff := 8 * time.Second

	cases := []struct {
		step   int
		expect time.Duration // exponential base before jitter
	}{
		{step: 0, expect: 1 * time.Second},
		{step: 1, expect: 2 * time.Second},
		{step: 2, expect: 4 * time.Second},
		{step: 3, expect: 8 * time.Second},
		{step: 4, expect: 8 * time.Second},
	}

	for _, c := range cases {
		for range 500 {
			got := JitteredBackoff(base, maxBackoff, c.step)
			low := time.Duration(float64(c.expect) * 0.75)
			high := time.Duration(float64(c.expect) * 1.25)
			if high > maxBackoff {
				high = maxBackoff
			}
			if got < low || got > high {
				t.Fatalf("step %d: backoff %v outside expected jittered band [%v, %v]",
					c.step, got, low, high)
			}
		}
	}
}

// TestJitteredBackoffStaysPositiveAndCapped is the overflow regression: for step
// numbers well past the point where base * (1 << step) wraps int64 nanoseconds
// negative, the result must remain strictly positive and within the cap.
func TestJitteredBackoffStaysPositiveAndCapped(t *testing.T) {
	base := 5 * time.Minute
	maxBackoff := time.Hour

	for _, step := range []int{0, 10, 33, 63, 64, 128, 10000, 1 << 20} {
		for range 100 {
			got := JitteredBackoff(base, maxBackoff, step)
			if got <= 0 {
				t.Fatalf("step %d: backoff must be strictly positive, got %v", step, got)
			}
			if got > maxBackoff {
				t.Fatalf("step %d: backoff %v exceeds cap %v", step, got, maxBackoff)
			}
		}
	}
}

// TestJitteredBackoffDegenerateInputs verifies the guards for a non-positive
// base, a non-positive cap, a base above the cap, and a negative step.
func TestJitteredBackoffDegenerateInputs(t *testing.T) {
	cases := []struct {
		name       string
		base       time.Duration
		maxBackoff time.Duration
		step       int
		maxAllowed time.Duration
	}{
		{name: "zero base", base: 0, maxBackoff: time.Minute, step: 5, maxAllowed: time.Minute},
		{
			name:       "negative base",
			base:       -time.Second,
			maxBackoff: time.Minute,
			step:       3,
			maxAllowed: time.Minute,
		},
		{
			name:       "zero cap",
			base:       2 * time.Second,
			maxBackoff: 0,
			step:       4,
			maxAllowed: 2 * time.Second,
		},
		{
			name:       "base above cap",
			base:       time.Hour,
			maxBackoff: time.Second,
			step:       6,
			maxAllowed: time.Second,
		},
		// A negative step is treated as step 0, so the slot is the base plus at
		// most the +25% jitter.
		{
			name:       "negative step",
			base:       2 * time.Second,
			maxBackoff: time.Minute,
			step:       -3,
			maxAllowed: 2500 * time.Millisecond,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for range 200 {
				got := JitteredBackoff(c.base, c.maxBackoff, c.step)
				if got <= 0 {
					t.Fatalf("expected strictly positive backoff, got %v", got)
				}
				if got > c.maxAllowed {
					t.Fatalf("expected backoff within %v, got %v", c.maxAllowed, got)
				}
			}
		})
	}
}

// TestJitteredBackoffDesynchronizes demonstrates why the jitter exists: repeated
// computations of the same slot spread out instead of returning one repeated
// value, so a fleet retrying against the same endpoint does not synchronize.
func TestJitteredBackoffDesynchronizes(t *testing.T) {
	const samples = 1000
	distinct := make(map[time.Duration]struct{}, samples)
	for range samples {
		distinct[JitteredBackoff(time.Second, time.Minute, 3)] = struct{}{}
	}

	if len(distinct) < samples/2 {
		t.Fatalf("expected a spread of delays, got only %d distinct values across %d samples",
			len(distinct), samples)
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

// TestJitteredBackoffSpreadsAtTheCap is the regression for jitter that was
// clamped to the ceiling instead of reflected under it. Clamping put roughly
// half of every capped draw on exactly maxBackoff, so a fleet whose schedules
// had all reached the ceiling - which is where a sustained outage puts it - fired
// half its retries at the same instant, the very synchronization the jitter is
// there to break up. It was caught by the integration scenario, whose two capped
// slots landed on exactly the cap on two of three platforms.
func TestJitteredBackoffSpreadsAtTheCap(t *testing.T) {
	base := time.Second
	maxBackoff := 8 * time.Second

	const samples = 1000
	atCap := 0
	distinct := make(map[time.Duration]struct{}, samples)
	minSeen := maxBackoff

	for range samples {
		// A step well past the point where the schedule reaches the ceiling.
		got := JitteredBackoff(base, maxBackoff, 10)
		if got <= 0 || got > maxBackoff {
			t.Fatalf("expected 0 < backoff <= %v, got %v", maxBackoff, got)
		}
		if got == maxBackoff {
			atCap++
		}
		if got < minSeen {
			minSeen = got
		}
		distinct[got] = struct{}{}
	}

	// The clamped implementation this replaces scored ~500 here.
	if atCap > samples/100 {
		t.Fatalf(
			"%d of %d capped slots landed on exactly %v; the jitter is clamped to the cap rather than spread under it",
			atCap,
			samples,
			maxBackoff,
		)
	}
	if len(distinct) < samples/2 {
		t.Fatalf("expected capped slots to spread, got only %d distinct values across %d samples",
			len(distinct), samples)
	}
	// The spread should reach into the band below the ceiling rather than
	// hovering against it.
	if minSeen > time.Duration(float64(maxBackoff)*0.9) {
		t.Fatalf(
			"capped slots never dropped meaningfully below the cap (smallest %v of %v)",
			minSeen,
			maxBackoff,
		)
	}
}
