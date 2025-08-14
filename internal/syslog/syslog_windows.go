//go:build windows

package syslog

import (
	"io"
	"strings"

	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc/eventlog"
)

type windowsSyslog struct {
	out io.Writer
	log *eventlog.Log
}

const infoEventId = 100
const warningEventId = 200
const errorEventId = 300

func (s *windowsSyslog) Write(data []byte) (int, error) {
	// Write to event log
	line := string(data)

	// Extract the message
	message := extractMessage(line)

	// Use different levels
	if strings.Contains(line, "[ERROR]") {
		s.log.Error(errorEventId, message)
	} else if strings.Contains(line, "[WARNING]") {
		s.log.Warning(warningEventId, message)
	} else {
		s.log.Info(infoEventId, message)
	}

	// Write to original output
	return s.out.Write(data)
}

func (s *windowsSyslog) Close() error {
	return s.log.Close()
}

func eventSourceExists(name string) (bool, error) {
	k, err := registry.OpenKey(
		registry.LOCAL_MACHINE,
		`SYSTEM\CurrentControlSet\Services\EventLog\Application\`+name,
		registry.READ,
	)

	if err != nil {
		if err == registry.ErrNotExist {
			return false, nil
		}
		return false, err
	}

	k.Close()

	return true, nil
}

func New(name string, out io.Writer) (Syslog, error) {

	exists, err := eventSourceExists(name)
	if err != nil {
		return nil, err
	}

	if !exists {
		err = eventlog.InstallAsEventCreate(name, eventlog.Info|eventlog.Error|eventlog.Warning)
		if err != nil {
			return nil, err
		}
	}

	log, err := eventlog.Open(name)
	if err != nil {
		return nil, err
	}

	syslogger := &windowsSyslog{
		out: out,
		log: log,
	}

	return syslogger, nil
}
