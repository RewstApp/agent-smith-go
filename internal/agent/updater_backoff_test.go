package agent

import (
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
)

// newBackoffTestRunner builds a runner wired for schedule inspection only; the
// updater is never run.
func newBackoffTestRunner(
	interval time.Duration,
	maxRetries int,
	base time.Duration,
) *AutoUpdateRunner {
	return NewAutoUpdateRunner(hclog.NewNullLogger(), &mockUpdater{}, interval, maxRetries, base)
}

// TestUpdateMaxRetryBackoff_CapSelection verifies the ceiling is the lower of the
// absolute DefaultUpdateMaxRetryBackoff and a quarter of the check interval, so
// the retry schedule stays nested inside the interval it belongs to.
func TestUpdateMaxRetryBackoff_CapSelection(t *testing.T) {
	cases := []struct {
		name     string
		interval time.Duration
		expected time.Duration
	}{
		{
			name:     "production interval uses absolute cap",
			interval: defaultUpdateInterval,
			expected: DefaultUpdateMaxRetryBackoff,
		},
		{
			name:     "short integration interval uses interval fraction",
			interval: 30 * time.Second,
			expected: 7500 * time.Millisecond,
		},
		{
			name:     "interval just above cap boundary uses absolute cap",
			interval: 8 * time.Hour,
			expected: DefaultUpdateMaxRetryBackoff,
		},
		{
			name:     "non-positive interval uses absolute cap",
			interval: 0,
			expected: DefaultUpdateMaxRetryBackoff,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := updateMaxRetryBackoff(c.interval); got != c.expected {
				t.Fatalf("expected cap %v for interval %v, got %v", c.expected, c.interval, got)
			}
		})
	}
}

// TestUpdateRetryBackoff_ProductionScheduleGrowsAndCaps verifies the production
// schedule (base 5m, 48h interval) still doubles as before for the early
// attempts and flattens at the documented 1h ceiling instead of growing to the
// 42-hour sleep the uncapped shift produced by attempt 10. Jitter is bounded to
// ±25%, so each slot is asserted within that band rather than exactly.
func TestUpdateRetryBackoff_ProductionScheduleGrowsAndCaps(t *testing.T) {
	runner := newBackoffTestRunner(defaultUpdateInterval, defaultMaxRetries, defaultBaseBackoff)

	cases := []struct {
		attempt  int
		expected time.Duration // exponential base before jitter
	}{
		{attempt: 0, expected: 5 * time.Minute},
		{attempt: 1, expected: 10 * time.Minute},
		{attempt: 2, expected: 20 * time.Minute},
		{attempt: 3, expected: 40 * time.Minute},
		{attempt: 4, expected: DefaultUpdateMaxRetryBackoff},
		{attempt: 10, expected: DefaultUpdateMaxRetryBackoff},
	}

	for _, c := range cases {
		// Sample repeatedly so jitter variance is exercised.
		for range 500 {
			got := runner.retryBackoff(c.attempt)
			low := time.Duration(float64(c.expected) * 0.75)
			high := time.Duration(float64(c.expected) * 1.25)
			if high > DefaultUpdateMaxRetryBackoff {
				high = DefaultUpdateMaxRetryBackoff
			}
			if got < low || got > high {
				t.Fatalf("attempt %d: backoff %v outside expected jittered band [%v, %v]",
					c.attempt, got, low, high)
			}
		}
	}
}

// TestUpdateRetryBackoff_AbsurdMaxRetriesStayPositiveAndCapped is the core
// regression for the uncapped/overflowing backoff: across the full attempt range
// of a deliberately absurd maxRetries — well past the point where
// baseBackoff * (1 << attempt) overflows int64 nanoseconds into a negative
// duration — every slot must stay strictly positive and within the cap, so
// time.After can never fire immediately and the retry loop can never busy-spin.
func TestUpdateRetryBackoff_AbsurdMaxRetriesStayPositiveAndCapped(t *testing.T) {
	for _, maxRetries := range []int{64, 128, 1000} {
		runner := newBackoffTestRunner(defaultUpdateInterval, maxRetries, defaultBaseBackoff)

		for attempt := range runner.maxRetries {
			got := runner.retryBackoff(attempt)
			if got <= 0 {
				t.Fatalf("maxRetries %d, attempt %d: backoff must be strictly positive, got %v",
					maxRetries, attempt, got)
			}
			if got > runner.maxBackoff {
				t.Fatalf("maxRetries %d, attempt %d: backoff %v exceeds cap %v",
					maxRetries, attempt, got, runner.maxBackoff)
			}
		}
	}

	// Spot-check attempt numbers far beyond any configurable retry count.
	runner := newBackoffTestRunner(defaultUpdateInterval, defaultMaxRetries, defaultBaseBackoff)
	for _, attempt := range []int{63, 64, 100, 10000, 1 << 20} {
		got := runner.retryBackoff(attempt)
		if got <= 0 || got > runner.maxBackoff {
			t.Fatalf(
				"attempt %d: expected 0 < backoff <= %v, got %v",
				attempt,
				runner.maxBackoff,
				got,
			)
		}
	}
}

// TestUpdateRetryBackoff_ShortIntervalStaysWithinInterval verifies that the
// retry budget of an agent built with the shortened integration ldflags still
// fits inside its own check interval rather than outliving it.
func TestUpdateRetryBackoff_ShortIntervalStaysWithinInterval(t *testing.T) {
	interval := 30 * time.Second
	runner := newBackoffTestRunner(interval, 5, time.Second)

	var total time.Duration
	for attempt := range runner.maxRetries {
		got := runner.retryBackoff(attempt)
		if got <= 0 || got > runner.maxBackoff {
			t.Fatalf(
				"attempt %d: expected 0 < backoff <= %v, got %v",
				attempt,
				runner.maxBackoff,
				got,
			)
		}
		total += got
	}

	if total > interval {
		t.Fatalf("full retry budget %v outlives the check interval %v", total, interval)
	}
}

// TestUpdateRetryBackoff_Desynchronizes demonstrates the fleet-wide effect the
// jitter exists for: many independent computations of the same retry slot
// produce a distribution of delays rather than one repeated value, so agents
// that fail against the same endpoint at the same moment do not retry in
// lockstep.
func TestUpdateRetryBackoff_Desynchronizes(t *testing.T) {
	runner := newBackoffTestRunner(defaultUpdateInterval, defaultMaxRetries, defaultBaseBackoff)

	const samples = 1000
	distinct := make(map[time.Duration]struct{}, samples)
	for range samples {
		distinct[runner.retryBackoff(2)] = struct{}{}
	}

	// The jitter is continuous, so near-total uniqueness is expected; assert a
	// conservative floor that a fixed (or coarsely quantized) schedule fails.
	if len(distinct) < samples/2 {
		t.Fatalf("expected a spread of retry delays, got only %d distinct values across %d samples",
			len(distinct), samples)
	}
}

// TestUpdateRetryBackoff_NonPositiveInputsFallBackToDefaults verifies the guards
// that keep a misconfigured base or cap from producing a zero or negative slot.
func TestUpdateRetryBackoff_NonPositiveInputsFallBackToDefaults(t *testing.T) {
	runner := newBackoffTestRunner(0, defaultMaxRetries, 0)
	runner.maxBackoff = 0

	for _, attempt := range []int{0, 5, 100} {
		got := runner.retryBackoff(attempt)
		if got <= 0 {
			t.Fatalf(
				"attempt %d: expected positive backoff with defaulted inputs, got %v",
				attempt,
				got,
			)
		}
		if got > DefaultUpdateMaxRetryBackoff {
			t.Fatalf("attempt %d: expected backoff within defaulted cap %v, got %v",
				attempt, DefaultUpdateMaxRetryBackoff, got)
		}
	}
}

// TestUpdateRetryBackoff_BaseAboveCapClamped verifies that a base delay larger
// than the cap (an ldflags build with a long base and a short interval) still
// produces a bounded, positive slot.
func TestUpdateRetryBackoff_BaseAboveCapClamped(t *testing.T) {
	runner := newBackoffTestRunner(10*time.Second, 5, time.Hour)

	for attempt := range runner.maxRetries {
		got := runner.retryBackoff(attempt)
		if got <= 0 || got > runner.maxBackoff {
			t.Fatalf(
				"attempt %d: expected 0 < backoff <= %v, got %v",
				attempt,
				runner.maxBackoff,
				got,
			)
		}
	}
}
