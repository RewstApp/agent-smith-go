//go:build darwin || linux

package syslog

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const (
	// syslogCommandTimeout bounds each `logger` invocation. Forwarding happens
	// synchronously inside Write, which hclog calls from whatever goroutine is
	// logging (MQTT handling, command workers, the plugin supervisor), so an
	// unbounded exec.Command here meant a wedged syslog daemon - or a `logger`
	// blocked writing to /dev/log - froze that goroutine forever, with no
	// timeout to recover: the same failure class already fixed for the
	// systemctl/launchctl/sc query shell-outs. Five seconds is enormously
	// generous for a one-shot that normally completes in single-digit
	// milliseconds, and short enough that it needs no -ldflags override the way
	// the multi-minute service timeouts do.
	syslogCommandTimeout = 5 * time.Second

	// syslogSuppressWindow is how long forwarding is skipped after a failed or
	// timed-out `logger` call. Bounding a single call is not enough on its own:
	// `logger` runs once per log line, so a hung daemon would still cost every
	// line the full syslogCommandTimeout, and hclog serializes writers - all
	// logging on the agent would crawl. After a failure the agent stops shelling
	// out entirely for this window and lets one line through afterwards as a
	// probe, so a degraded daemon costs at most one timeout per window while the
	// on-disk log keeps every line at full speed.
	syslogSuppressWindow = time.Minute

	// syslogNoteTimeFormat matches hclog's default timestamp format so the
	// diagnostics this writer inserts into the log file line up with the entries
	// around them.
	syslogNoteTimeFormat = "2006-01-02T15:04:05.000Z0700"
)

type commandRunner interface {
	Run(priority, source, message string) error
}

type loggerCommandRunner struct {
	// binary is the executable Run invokes. Empty selects "logger"; tests point
	// it at a fixture to exercise the timeout and failure paths without
	// depending on a real syslog daemon.
	binary string

	// timeout overrides syslogCommandTimeout when positive. Tests only.
	timeout time.Duration
}

func (r *loggerCommandRunner) Run(priority, source, message string) error {
	binary := r.binary
	if binary == "" {
		binary = "logger"
	}

	timeout := r.timeout
	if timeout <= 0 {
		timeout = syslogCommandTimeout
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, "-p", priority, "-t", source, message)
	killProcessGroupOnCancel(cmd)

	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf(
				"logger -p %s -t %s timed out after %s: %s",
				priority, source, timeout, out,
			)
		}
		return fmt.Errorf("logger -p %s -t %s failed: %w: %s", priority, source, err, out)
	}

	return nil
}

// killProcessGroupOnCancel places the command in its own process group and
// kills the whole group when the context expires.
//
// Killing only `logger` itself is not enough to unblock the caller: anything it
// spawned inherits the pipe CombinedOutput reads, so cmd.Wait keeps blocking
// until that descendant exits - the timeout would elapse and Run would still
// hang, which is the bug this bound exists to prevent. This mirrors
// interpreter.configureProcessGroup, which tears down command trees for the
// same reason.
func killProcessGroupOnCancel(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// A negative pid targets the entire process group led by `logger`.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

// unixSyslog forwards each log line to the system logger and writes it to the
// agent's own log file. The two writes are independent by design; see Write.
type unixSyslog struct {
	out    io.Writer
	source string
	runner commandRunner

	// suppressWindow overrides syslogSuppressWindow when positive. Tests only.
	suppressWindow time.Duration

	// mu guards the forwarding health state below. Write is called from every
	// goroutine that logs; hclog serializes its own calls, but nothing
	// guarantees this writer is only ever reached through one logger.
	mu       sync.Mutex
	degraded bool
	retryAt  time.Time
	failures int
	skipped  int
}

// Write forwards the line to syslog on a best-effort basis and always writes it
// to the underlying log file.
//
// The syslog outcome deliberately never reaches the caller. This used to return
// (0, err) as soon as `logger` failed, which skipped the on-disk write
// entirely - a transient syslog hiccup (daemon restart, missing binary) also
// punched a hole in the log file operators read afterwards, precisely when
// something was already going wrong. hclog has no way to react to a writer
// error beyond discarding the line, so surfacing one buys nothing and costs the
// record. Syslog failures are instead swallowed and reported into the log file
// itself, once per transition, the same "counted, not fatal" treatment plugin
// notify failures get. The returned (n, err) always describe the on-disk write,
// so a genuine short write or file error still surfaces.
func (s *unixSyslog) Write(data []byte) (int, error) {
	if note := s.forward(string(data)); note != "" {
		// Best effort: a diagnostic about logging must never displace the log
		// line it is describing.
		_, _ = io.WriteString(s.out, note)
	}

	return s.out.Write(data)
}

func (s *unixSyslog) Close() error {
	return nil
}

// forward sends one line to the system logger unless forwarding is currently
// suppressed, and returns the diagnostic line - empty unless the health state
// changed - to record in the log file.
func (s *unixSyslog) forward(line string) string {
	if !s.beginForward() {
		return ""
	}

	message := syslogMessage(s.source, extractMessage(line))
	err := s.runner.Run(priorityForLine(line), s.source, message)

	return s.recordResult(err)
}

// beginForward reports whether this line should shell out to `logger`. While
// forwarding is suppressed after a failure it returns false; the first line
// after the window elapses is allowed through as a probe.
func (s *unixSyslog) beginForward() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.degraded && time.Now().Before(s.retryAt) {
		s.skipped++
		return false
	}

	return true
}

// recordResult folds the outcome of one `logger` call into the forwarding
// health state and returns the diagnostic to write to the log file: one line
// when forwarding starts failing, one when it recovers, and nothing in between
// so a lasting syslog outage cannot flood the log it is already the only copy
// of.
func (s *unixSyslog) recordResult(err error) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err == nil {
		if !s.degraded {
			return ""
		}

		note := s.note(fmt.Sprintf(
			"syslog forwarding recovered after %d failure(s); %d line(s) reached this log only",
			s.failures, s.skipped,
		))

		s.degraded = false
		s.failures = 0
		s.skipped = 0
		s.retryAt = time.Time{}

		return note
	}

	s.failures++
	s.retryAt = time.Now().Add(s.resolveSuppressWindow())

	if s.degraded {
		// Already reported; this failed probe only extends the window.
		return ""
	}

	s.degraded = true

	return s.note(fmt.Sprintf(
		"syslog forwarding failed, suppressing it for %s (log lines still reach this file): %v",
		s.resolveSuppressWindow(), err,
	))
}

// note formats a diagnostic the way hclog formats its own entries so it does
// not break tooling that parses the log file.
func (s *unixSyslog) note(message string) string {
	return fmt.Sprintf(
		"%s [WARN]  %s: %s\n",
		time.Now().Format(syslogNoteTimeFormat), s.source, message,
	)
}

func (s *unixSyslog) resolveSuppressWindow() time.Duration {
	if s.suppressWindow > 0 {
		return s.suppressWindow
	}

	return syslogSuppressWindow
}

func priorityForLine(line string) string {
	switch levelForLine(line) {
	case levelError:
		return "daemon.err"
	case levelWarning:
		return "daemon.warning"
	default:
		return "daemon.info"
	}
}

func New(name string, out io.Writer) (Syslog, error) {
	return newWithRunner(name, out, &loggerCommandRunner{}), nil
}

func newWithRunner(name string, out io.Writer, runner commandRunner) Syslog {
	return &unixSyslog{out: out, source: name, runner: runner}
}
