package main

import (
	"io"
	"sync"

	"github.com/RewstApp/agent-smith-go/internal/syslog"
)

// logSink is the writer the agent's logger writes through: the log file, or
// the system-log forwarder wrapped around it when syslog is on. The forwarder
// always writes the file too, so switching it changes only whether lines also
// reach the system log. The device twin's syslog setting switches it while the
// service runs (sc-132991); before that the logger was built on one writer or
// the other at startup and only a reinstall changed it.
type logSink struct {
	// mu is held for reading by every write and for writing by a switch, so a
	// forwarder is closed only once no write can still be going through it.
	mu   sync.RWMutex
	file io.Writer
	sys  syslog.Syslog
	// open builds the forwarder around the file; syslog.New in the service.
	open func(out io.Writer) (syslog.Syslog, error)
}

func newLogSink(file io.Writer, open func(out io.Writer) (syslog.Syslog, error)) *logSink {
	return &logSink{file: file, open: open}
}

func (s *logSink) Write(p []byte) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.sys != nil {
		return s.sys.Write(p)
	}
	return s.file.Write(p)
}

// SetSyslog turns forwarding to the system log on or off. Turning it on opens
// the forwarder (neither platform's opens the system log before its first
// write, so this does not fail in practice); turning it off closes it after
// the switch, so no line is lost or written twice. A switch to the state
// already in effect does nothing.
func (s *logSink) SetSyslog(on bool) error {
	s.mu.Lock()
	if on == (s.sys != nil) {
		s.mu.Unlock()
		return nil
	}
	var old syslog.Syslog
	if on {
		sys, err := s.open(s.file)
		if err != nil {
			s.mu.Unlock()
			return err
		}
		s.sys = sys
	} else {
		old, s.sys = s.sys, nil
	}
	s.mu.Unlock()
	if old != nil {
		return old.Close()
	}
	return nil
}

// Syslog reports whether lines are forwarded to the system log.
func (s *logSink) Syslog() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sys != nil
}
