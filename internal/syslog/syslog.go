package syslog

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

type Syslog interface {
	Write(p []byte) (int, error)
	Close() error
}

// syslogSuppressWindow is how long forwarding to the system log is skipped
// after a failed attempt, on every platform. Bounding a single attempt is not
// enough on its own: the forward runs once per log line, so a sink that keeps
// failing would cost every line the full price of the failure (a `logger`
// timeout on Linux and macOS, an RPC round trip to a wedged EventLog service
// on Windows) and, with hclog serializing writers, all logging on the agent
// would crawl. After a failure the writer stops forwarding entirely for this
// window and lets one line through afterwards as a probe, so a degraded sink
// costs at most one failure per window while the on-disk log keeps every line
// at full speed.
const syslogSuppressWindow = time.Minute

// forwardHealth is the forwarding health state the platform writers share:
// whether forwarding is currently suppressed, when the next probe is due and
// the counts the recovery note reports. Each writer feeds it the outcome of
// one forward and writes the diagnostic it hands back into the log file. The
// state machine lives here, rather than once per writer, so the two sinks
// cannot drift apart: the Windows writer used to have none of it (sc-133002)
// and failed the whole service start instead.
type forwardHealth struct {
	// mu guards everything below. Write is called from every goroutine that
	// logs; hclog serializes its own calls, but nothing guarantees a writer is
	// only ever reached through one logger.
	mu       sync.Mutex
	degraded bool
	retryAt  time.Time
	failures int
	skipped  int

	// now overrides time.Now when set. Tests only: Go's monotonic clock on
	// Windows advances at the clock-interrupt rate, so a test that waits for
	// a real window to elapse is not deterministic there.
	now func() time.Time
}

func (h *forwardHealth) clock() time.Time {
	if h.now != nil {
		return h.now()
	}

	return time.Now()
}

// begin reports whether this line should be forwarded. While forwarding is
// suppressed after a failure it returns false; the first line after the window
// elapses is allowed through as a probe.
func (h *forwardHealth) begin() bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.degraded && h.clock().Before(h.retryAt) {
		h.skipped++
		return false
	}

	return true
}

// record folds the outcome of one forward into the health state and returns
// the diagnostic to write to the log file: one line when forwarding starts
// failing, one when it recovers, and nothing in between so a lasting outage
// cannot flood the log it is already the only copy of. what names the sink in
// the diagnostic ("syslog forwarding", "event log forwarding").
func (h *forwardHealth) record(what string, err error, window time.Duration) string {
	h.mu.Lock()
	defer h.mu.Unlock()

	if err == nil {
		if !h.degraded {
			return ""
		}

		note := fmt.Sprintf(
			"%s recovered after %d failure(s); %d line(s) reached this log only",
			what, h.failures, h.skipped,
		)

		h.degraded = false
		h.failures = 0
		h.skipped = 0
		h.retryAt = time.Time{}

		return note
	}

	h.failures++
	h.retryAt = h.clock().Add(window)

	if h.degraded {
		// Already reported; this failed probe only extends the window.
		return ""
	}

	h.degraded = true

	return fmt.Sprintf(
		"%s failed, suppressing it for %s (log lines still reach this file): %v",
		what, window, err,
	)
}

// logLevel is the severity a formatted log line carries, recovered from the
// level bracket hclog writes into it. Every platform maps this onto its own
// sink (syslog priorities on Linux/macOS, event log severities on Windows);
// the classification itself lives here so the three writers cannot drift apart
// again - each used to match the bracket text itself, and all three looked for
// "[WARNING]", which hclog never emits (it writes "[WARN] "), so warnings were
// silently logged as informational on every platform.
type logLevel int

const (
	levelInfo logLevel = iota
	levelWarning
	levelError
)

func levelForLine(line string) logLevel {
	switch {
	case strings.Contains(line, "[ERROR]"):
		return levelError
	// "[WARNING]" is not what hclog emits, but it costs nothing to keep
	// honouring the spelling this code has always looked for.
	case strings.Contains(line, "[WARN]"), strings.Contains(line, "[WARNING]"):
		return levelWarning
	default:
		return levelInfo
	}
}

func extractMessage(line string) string {
	idx := strings.Index(line, "]")
	if idx < 0 || idx+2 > len(line) {
		return line
	}
	return line[idx+2:]
}
