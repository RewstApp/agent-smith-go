//go:build e2e

package e2e

import (
	"fmt"
	"math"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
)

const (
	// itVersionTag is the integration build's own version. A release served
	// under it ends the check at "No updates available", so a recovered
	// endpoint is observed without the agent downloading anything.
	itVersionTag = "v0.0.0-it"

	// retryCapSeconds is the integration build's retry ceiling: a quarter of
	// its 30 s update interval (updateBackoffIntervalDivisor).
	retryCapSeconds = 7.5

	// updateInterval is the integration build's check interval; the first
	// check happens this long after the service starts.
	updateInterval = 30 * time.Second
)

// assetName is the release asset the updater selects on this platform
// (internal/agent/updater_<os>.go).
func assetName(t *testing.T) string {
	t.Helper()
	switch runtime.GOOS {
	case "linux":
		return "rewst_agent_config.linux.bin"
	case "darwin":
		return "rewst_agent_config.mac-os.bin"
	case "windows":
		return "rewst_agent_config.win.exe"
	}
	t.Fatalf("no release asset name for %s", runtime.GOOS)
	return ""
}

// TestAutoUpdate drives the integration build's auto-updater against the
// in-process release stub: the retry schedule against a failing endpoint and
// its recovery mid-sequence, the schedule's shape, a service stop during a
// backoff, and the two digest rejections (sc-106110, sc-112402).
func TestAutoUpdate(t *testing.T) {
	h := New(t)
	h.Install()

	stub := newReleaseStub(t, itVersionTag, "", "")
	h.SetReleaseURL(stub.URL())
	h.Update(h.ITBinary, true)

	t.Run("RetrySucceedsAfterFailures", func(t *testing.T) {
		// Four retries in: 2 s, 4 s, then two at the 7.5 s cap, so the cap is
		// visible twice before the endpoint recovers, and two slots remain
		// for the recovery to land in.
		h.WaitForLogCount("Retrying update", 3, updateInterval+2*time.Minute)
		stub.SetOK(true)
		h.WaitForLogCount("Update succeeded on retry", 0, 2*time.Minute)
		if n := h.LogCount("All retries exhausted"); n != 0 {
			t.Errorf("the retry budget was exhausted %d time(s) before the endpoint recovered", n)
		}
		served := 0
		for _, r := range stub.Requests() {
			if r.OK {
				served++
			}
		}
		if served == 0 {
			t.Error("the stub never served the release the successful retry reported")
		}
	})

	t.Run("ScheduleIsCappedJitteredAndNeverImmediate", func(t *testing.T) {
		slots := parseRetrySlots(t, h.LogLines("Retrying update"))
		for _, f := range assertRetrySchedule(slots, retryCapSeconds) {
			t.Error(f)
		}
	})

	t.Run("StopDuringBackoffIsPrompt", func(t *testing.T) {
		stub.SetOK(false)
		retrying := h.LogCount("Retrying update")
		stopped := h.LogCount("Auto updater stopped")
		// The next check, up to one interval away, fails and enters a backoff.
		h.WaitForLogCount("Retrying update", retrying, updateInterval+time.Minute)
		started := time.Now()
		h.StopService()
		h.WaitForLogCount("Auto updater stopped", stopped, time.Minute)
		elapsed := time.Since(started)
		if elapsed > 20*time.Second {
			t.Errorf(
				"the auto updater stopped only after %s, long enough to have waited out its retry schedule",
				elapsed.Round(100*time.Millisecond),
			)
		}
		t.Logf(
			"auto updater stopped %s after the stop was issued, mid-backoff",
			elapsed.Round(100*time.Millisecond),
		)
	})

	h.ClearReleaseURL()
	h.Update(h.Binary, false)

	t.Run("TamperedDigestIsRejected", func(t *testing.T) {
		tampered := newReleaseStub(t, "v99.0.0", assetName(t), "sha256:"+strings.Repeat("0", 64))
		tampered.SetOK(true)
		h.SetReleaseURL(tampered.URL())
		backoffs := h.LogCount("Update failed, starting retry backoff")
		h.Update(h.ITBinary, true)
		installed := sha256File(t, h.ExePath())

		h.WaitForLogCount("checksum mismatch for", 0, updateInterval+2*time.Minute)
		if h.LogCount("Update failed, starting retry backoff") <= backoffs {
			t.Error("the rejected download did not start the retry backoff")
		}
		if h.LogCount("Updating agent") == 0 {
			t.Error("the agent never decided to update, so the digest check was not reached")
		}
		if tampered.Downloads() == 0 {
			t.Error("the stub never served the asset the agent reported a mismatch for")
		}
		if got := sha256File(t, h.ExePath()); got != installed {
			t.Errorf(
				"the installed executable changed after a tampered download: %s -> %s",
				installed,
				got,
			)
		}
		h.ClearReleaseURL()
		h.Update(h.Binary, false)
	})

	t.Run("MissingDigestIsRejected", func(t *testing.T) {
		missing := newReleaseStub(t, "v99.0.0", assetName(t), "")
		missing.SetOK(true)
		h.SetReleaseURL(missing.URL())
		backoffs := h.LogCount("Update failed, starting retry backoff")
		h.Update(h.ITBinary, true)
		installed := sha256File(t, h.ExePath())

		h.WaitForLogCount("failed to verify installer for", 0, updateInterval+2*time.Minute)
		if h.LogCount("no usable sha256 digest") == 0 {
			t.Error("the rejection did not name the missing digest")
		}
		if h.LogCount("Update failed, starting retry backoff") <= backoffs {
			t.Error("the rejected download did not start the retry backoff")
		}
		if got := sha256File(t, h.ExePath()); got != installed {
			t.Errorf(
				"the installed executable changed after an unverifiable download: %s -> %s",
				installed,
				got,
			)
		}
		h.ClearReleaseURL()
		h.Update(h.Binary, false)
	})
}

// retrySlot is one "Retrying update: attempt=N backoff=D" log line.
type retrySlot struct {
	Attempt int
	Backoff float64 // seconds
	Time    time.Time
}

var retryLine = regexp.MustCompile(`^(\S+) .*Retrying update: attempt=(\d+) backoff=(\S+)`)

// parseRetrySlots turns the retry log lines into slots, failing on a line that
// does not parse: the assertion is only as good as its reading of the log.
func parseRetrySlots(t *testing.T, lines []string) []retrySlot {
	t.Helper()
	var slots []retrySlot
	for _, ln := range lines {
		m := retryLine.FindStringSubmatch(ln)
		if m == nil {
			t.Fatalf("could not parse a retry line: %s", ln)
		}
		stamp, err := time.Parse(hclog.TimeFormat, m[1])
		if err != nil {
			t.Fatalf("could not parse the timestamp of: %s (%v)", ln, err)
		}
		attempt, err := strconv.Atoi(m[2])
		if err != nil {
			t.Fatalf("could not parse the attempt of: %s (%v)", ln, err)
		}
		backoff, err := time.ParseDuration(m[3])
		if err != nil {
			t.Fatalf("could not parse the backoff of: %s (%v)", ln, err)
		}
		slots = append(slots, retrySlot{Attempt: attempt, Backoff: backoff.Seconds(), Time: stamp})
	}
	return slots
}

// assertRetrySchedule returns every way the observed schedule departs from the
// one the agent documents: every slot strictly positive and at or under the
// cap, the slots not all equal (jitter), the capped slots not all equal
// (jitter applied at the cap too), the schedule growing from its first slot,
// and consecutive attempts never back to back nor sooner than the backoff the
// previous one announced. It mirrors the workflow's former assertion script.
func assertRetrySchedule(slots []retrySlot, capSeconds float64) []string {
	var failures []string
	if len(slots) < 3 {
		return []string{
			fmt.Sprintf("expected at least three retry slots to inspect, got %d", len(slots)),
		}
	}
	round := func(v float64) float64 { return math.Round(v*1000) / 1000 }
	distinct := map[float64]bool{}
	nearCap := map[float64]bool{}
	nearCapCount := 0
	maxBackoff := 0.0
	for _, s := range slots {
		if s.Backoff <= 0 {
			failures = append(
				failures,
				fmt.Sprintf(
					"attempt %d: backoff %.3fs is not strictly positive",
					s.Attempt,
					s.Backoff,
				),
			)
		}
		if s.Backoff > capSeconds+0.05 {
			failures = append(
				failures,
				fmt.Sprintf(
					"attempt %d: backoff %.3fs exceeds the %.1fs cap",
					s.Attempt,
					s.Backoff,
					capSeconds,
				),
			)
		}
		distinct[round(s.Backoff)] = true
		if s.Backoff >= capSeconds*0.75 {
			nearCapCount++
			nearCap[round(s.Backoff)] = true
		}
		maxBackoff = math.Max(maxBackoff, s.Backoff)
	}
	if len(distinct) < 2 {
		failures = append(
			failures,
			fmt.Sprintf(
				"every retry slot was %.3fs - the schedule is not jittered",
				slots[0].Backoff,
			),
		)
	}
	if nearCapCount >= 2 && len(nearCap) < 2 {
		failures = append(
			failures,
			"the capped slots all measured the same - the jitter is not being applied at the cap",
		)
	}
	if maxBackoff <= slots[0].Backoff {
		failures = append(
			failures,
			fmt.Sprintf(
				"the schedule never grew (first %.3fs, largest %.3fs)",
				slots[0].Backoff,
				maxBackoff,
			),
		)
	}
	for i := 0; i+1 < len(slots); i++ {
		if slots[i+1].Attempt != slots[i].Attempt+1 {
			continue
		}
		gap := slots[i+1].Time.Sub(slots[i].Time).Seconds()
		if gap < 1 {
			failures = append(
				failures,
				fmt.Sprintf(
					"attempts %d and %d arrived %.3fs apart - back to back",
					slots[i].Attempt,
					slots[i+1].Attempt,
					gap,
				),
			)
		}
		if gap < slots[i].Backoff*0.8 {
			failures = append(
				failures,
				fmt.Sprintf(
					"attempt %d logged a %.3fs backoff but the next retry came after %.3fs",
					slots[i].Attempt,
					slots[i].Backoff,
					gap,
				),
			)
		}
	}
	return failures
}
