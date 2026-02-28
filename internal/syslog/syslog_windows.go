//go:build windows

package syslog

import (
	"errors"
	"io"
	"strings"
	"syscall"

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

type windowsSyslog struct {
	out io.Writer
	log eventLogger
}

const (
	infoEventId    = 100
	warningEventId = 200
	errorEventId   = 300
)

func (s *windowsSyslog) Write(data []byte) (int, error) {
	// Write to event log
	line := string(data)

	// Extract the message
	message := extractMessage(line)

	// Use different levels
	if strings.Contains(line, "[ERROR]") {
		_ = s.log.Error(errorEventId, message) // Best effort logging
	} else if strings.Contains(line, "[WARNING]") {
		_ = s.log.Warning(warningEventId, message) // Best effort logging
	} else {
		_ = s.log.Info(infoEventId, message) // Best effort logging
	}

	// Write to original output
	return s.out.Write(data)
}

func (s *windowsSyslog) Close() error {
	return s.log.Close()
}

func New(name string, out io.Writer) (Syslog, error) {
	return newWithFactory(name, out, &windowsEventLogFactory{})
}

func newWithFactory(name string, out io.Writer, factory eventLogFactory) (Syslog, error) {
	k, err := factory.OpenKey(name)
	if err == nil {
		err = k.Close()
		if err != nil {
			return nil, err
		}
	} else if errors.Is(err, registry.ErrNotExist) ||
		errors.Is(err, syscall.ERROR_PATH_NOT_FOUND) {
		if err = factory.Install(name); err != nil {
			return nil, err
		}
	} else {
		return nil, err
	}

	log, err := factory.Open(name)
	if err != nil {
		return nil, err
	}

	return &windowsSyslog{out: out, log: log}, nil
}
