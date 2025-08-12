//go:build windows

package syslog

import (
	"io"
	"strings"

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
	message := string(data)

	// Use different levels
	if strings.Contains(message, "[ERROR]") {
		s.log.Error(errorEventId, message)
	} else if strings.Contains(message, "[WARNING]") {
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

func New(name string, out io.Writer) (Syslog, error) {

	err := eventlog.InstallAsEventCreate(name, eventlog.Info|eventlog.Error|eventlog.Warning)
	if err != nil {
		return nil, err
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
