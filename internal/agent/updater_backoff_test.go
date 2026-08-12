package agent

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/utils"
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

// TestResolveLatestReleaseUrl_ReleasedBuildIgnoresOverrideFile pins the gate on
// the override seam: with no ldflags injection (the released build) the file is
// never consulted, so the update source of a shipped agent cannot be redirected
// by dropping a file on an endpoint.
func TestResolveLatestReleaseUrl_ReleasedBuildIgnoresOverrideFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(dir, "release_url_override"),
		[]byte("http://127.0.0.1:8765/releases/latest"),
		0o644,
	); err != nil {
		t.Fatalf("failed to seed override file: %v", err)
	}

	const defaultUrl = "https://api.github.com/repos/rewstapp/agent-smith-go/releases/latest"
	got := resolveLatestReleaseUrl(hclog.NewNullLogger(), dir, defaultUrl)
	if got != defaultUrl {
		t.Fatalf("expected released build to keep %q, got %q", defaultUrl, got)
	}
}

// TestResolveLatestReleaseUrl_IntegrationBuildReadsOverrideFile covers the
// integration-test build: the override is honored when present, and every
// degenerate case (missing, empty, whitespace-only) falls back to the compiled-in
// endpoint so a fixture that failed to write the file leaves the agent updating
// normally rather than silently not updating at all.
func TestResolveLatestReleaseUrl_IntegrationBuildReadsOverrideFile(t *testing.T) {
	const (
		defaultUrl = "https://api.github.com/repos/rewstapp/agent-smith-go/releases/latest"
		stubUrl    = "http://127.0.0.1:8765/releases/latest"
	)

	previous := releaseUrlOverrideFileStr
	releaseUrlOverrideFileStr = "release_url_override"
	t.Cleanup(func() { releaseUrlOverrideFileStr = previous })

	cases := []struct {
		name     string
		contents *string
		expected string
	}{
		{name: "missing file", contents: nil, expected: defaultUrl},
		{name: "empty file", contents: ptr(""), expected: defaultUrl},
		{name: "whitespace only", contents: ptr("  \n"), expected: defaultUrl},
		{name: "url", contents: ptr(stubUrl), expected: stubUrl},
		{name: "url with trailing newline", contents: ptr(stubUrl + "\n"), expected: stubUrl},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if c.contents != nil {
				path := filepath.Join(dir, releaseUrlOverrideFileStr)
				if err := os.WriteFile(path, []byte(*c.contents), 0o644); err != nil {
					t.Fatalf("failed to write override file: %v", err)
				}
			}

			got := resolveLatestReleaseUrl(hclog.NewNullLogger(), dir, defaultUrl)
			if got != c.expected {
				t.Fatalf("expected %q, got %q", c.expected, got)
			}
		})
	}
}

func ptr(s string) *string { return &s }

// TestAutoUpdateRunner_LogsStopDuringBackoff pins the record left behind when a
// stop lands while the runner is waiting out a retry backoff. That wait is the
// one place a stop has to interrupt a pending sleep, so an exit with no log line
// leaves the path the bounded, jittered schedule exists for unobservable - and
// the integration workflow reads exactly this line to prove the stop was prompt.
func TestAutoUpdateRunner_LogsStopDuringBackoff(t *testing.T) {
	var buf bytes.Buffer
	logger := utils.ConfigureLogger("test", &buf, utils.Info)
	mock := &mockUpdater{runErr: fmt.Errorf("release endpoint unavailable")}

	// A tick almost immediately, then a backoff long enough that only the stop
	// can end it. The cap is raised past the interval-derived default for the
	// same reason.
	runner := NewAutoUpdateRunner(logger, mock, time.Millisecond, 5, time.Hour)
	runner.maxBackoff = time.Hour

	runner.Start()

	// Wait until the runner is actually inside the backoff wait, so the stop
	// cannot be observed by the outer select instead.
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(buf.String(), "Retrying update") {
		if time.Now().After(deadline) {
			t.Fatal("runner never entered a retry backoff")
		}
		time.Sleep(5 * time.Millisecond)
	}

	stopped := make(chan struct{})
	go func() {
		runner.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return promptly during a backoff wait")
	}

	output := buf.String()
	if !strings.Contains(output, "Auto updater stopped") {
		t.Fatalf("expected a stop to be logged from the backoff wait, got %q", output)
	}
	if strings.Count(output, "Auto updater stopped") != 1 {
		t.Fatalf("expected the stop to be logged exactly once, got %q", output)
	}
}

// TestAutoUpdateRunner_LogsStopWhileIdle covers the other exit: a stop observed
// by the run loop's own select, between checks.
func TestAutoUpdateRunner_LogsStopWhileIdle(t *testing.T) {
	var buf bytes.Buffer
	logger := utils.ConfigureLogger("test", &buf, utils.Info)

	runner := NewAutoUpdateRunner(logger, &mockUpdater{}, time.Hour, 3, time.Millisecond)
	runner.Start()
	runner.Stop()

	output := buf.String()
	if strings.Count(output, "Auto updater stopped") != 1 {
		t.Fatalf("expected the stop to be logged exactly once, got %q", output)
	}
}
