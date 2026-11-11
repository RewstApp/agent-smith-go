//go:build windows

package syslog

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"syscall"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/utils"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc/eventlog"
)

type eventLogger interface {
	Info(eid uint32, msg string) error
	Warning(eid uint32, msg string) error
	Error(eid uint32, msg string) error
	Close() error
}

type eventLogFactory interface {
	OpenKey(name string) (io.Closer, error)
	Install(name string) error
	Open(name string) (eventLogger, error)
}

type windowsEventLogFactory struct{}

func (f *windowsEventLogFactory) OpenKey(name string) (io.Closer, error) {
	return registry.OpenKey(
		registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Services\EventLog\Application\`+name,
		registry.READ,
	)
}

func (f *windowsEventLogFactory) Install(name string) error {
	return eventlog.InstallAsEventCreate(name, eventlog.Info|eventlog.Error|eventlog.Warning)
}

func (f *windowsEventLogFactory) Open(name string) (eventLogger, error) {
	return eventlog.Open(name)
}

// windowsSyslog forwards each log line to the Application event log and
// writes it to the agent's own log file. The two writes are independent by
// design; see Write.
type windowsSyslog struct {
	out     io.Writer
	source  string
	factory eventLogFactory

	// suppressWindow overrides syslogSuppressWindow when positive. Tests only.
	suppressWindow time.Duration

	// mu guards log: the event source handle, nil until the source has been
	// registered and opened, and nil again after a report through it fails.
	mu  sync.Mutex
	log eventLogger

	health forwardHealth
}

const (
	infoEventId    = 100
	warningEventId = 200
	errorEventId   = 300
)

// Write forwards the line to the event log on a best-effort basis and always
// writes it to the underlying log file.
//
// The event-log outcome never reaches the caller, and - since sc-133002 -
// neither does the outcome of registering the event source. New used to open
// the source up front and fail when any of the three steps (reading the
// registry key, installing it, RegisterEventSource) failed, and the service
// then exited with LogFileError before it had logged a single line: the
// Windows endpoint restarted after `--update --syslog` and left an empty log
// file, with the failure recorded nowhere. The source is now opened on the
// first write instead, and a failure at any step is treated exactly like a
// failed `logger` call on Linux and macOS: the line still reaches the log
// file, the failure is recorded there once per transition in hclog's format,
// forwarding is suppressed for syslogSuppressWindow, and the registration is
// retried from the start on the next line after the window. The returned
// (n, err) always describe the on-disk write.
func (s *windowsSyslog) Write(data []byte) (int, error) {
	if note := s.forward(string(data)); note != "" {
		// Best effort: a diagnostic about logging must never displace the log
		// line it is describing.
		_, _ = io.WriteString(s.out, note)
	}

	return s.out.Write(data)
}

// forward reports one line to the event log unless forwarding is currently
// suppressed, and returns the diagnostic line - empty unless the health state
// changed - to record in the log file.
func (s *windowsSyslog) forward(line string) string {
	if !s.health.begin() {
		return ""
	}

	err := s.report(line)

	if note := s.health.record("event log forwarding", err, s.resolveSuppressWindow()); note != "" {
		// Formatted the way hclog formats its own entries so it does not break
		// tooling that parses the log file.
		return utils.HclogWarnLine(time.Now(), s.source, note)
	}

	return ""
}

// report writes one line to the event log, opening the event source first if
// it is not open. A failed report drops the handle so the next attempt starts
// over from registration: ReportEvent fails through a handle the EventLog
// service no longer knows (it restarted, or the source was removed) and no
// amount of retrying through that same handle would recover.
func (s *windowsSyslog) report(line string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.log == nil {
		log, err := openEventSource(s.source, s.factory)
		if err != nil {
			return err
		}
		s.log = log
	}

	message := extractMessage(line)

	var err error
	switch levelForLine(line) {
	case levelError:
		err = s.log.Error(errorEventId, message)
	case levelWarning:
		err = s.log.Warning(warningEventId, message)
	default:
		err = s.log.Info(infoEventId, message)
	}

	if err != nil {
		_ = s.log.Close()
		s.log = nil
		return fmt.Errorf("report event as %s: %w", s.source, err)
	}

	return nil
}

func (s *windowsSyslog) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.log == nil {
		return nil
	}

	err := s.log.Close()
	s.log = nil

	return err
}

func (s *windowsSyslog) resolveSuppressWindow() time.Duration {
	if s.suppressWindow > 0 {
		return s.suppressWindow
	}

	return syslogSuppressWindow
}

// openEventSource registers the event source if it is not registered and
// opens a handle to it.
func openEventSource(name string, factory eventLogFactory) (eventLogger, error) {
	if err := ensureEventSource(name, factory); err != nil {
		return nil, err
	}

	log, err := factory.Open(name)
	if err != nil {
		return nil, fmt.Errorf("register event source %s: %w", name, err)
	}

	return log, nil
}

// ensureEventSource makes sure the registry key that declares the event
// source exists, installing it when it does not.
func ensureEventSource(name string, factory eventLogFactory) error {
	k, err := factory.OpenKey(name)
	if err == nil {
		if err := k.Close(); err != nil {
			return fmt.Errorf("close event source key %s: %w", name, err)
		}
		return nil
	}
	if !errors.Is(err, registry.ErrNotExist) && !errors.Is(err, syscall.ERROR_PATH_NOT_FOUND) {
		return fmt.Errorf("read event source key %s: %w", name, err)
	}

	installErr := factory.Install(name)
	if installErr == nil {
		return nil
	}

	// InstallAsEventCreate refuses a key that already exists. If the key
	// appeared between the read above and the install - another agent process
	// for the same service registering the same name - the source is
	// registered, which is all this needed; only a key that is still missing
	// is a failure.
	if k, err := factory.OpenKey(name); err == nil {
		_ = k.Close()
		return nil
	}

	return fmt.Errorf("install event source %s: %w", name, installErr)
}

// New returns the event-log writer. It never fails: the event source is
// opened on the first write, and a failure there degrades forwarding rather
// than the service (see Write).
func New(name string, out io.Writer) (Syslog, error) {
	return newWithFactory(name, out, &windowsEventLogFactory{}), nil
}

func newWithFactory(name string, out io.Writer, factory eventLogFactory) Syslog {
	return &windowsSyslog{out: out, source: name, factory: factory}
}
