//go:build darwin

package syslog

import (
	"io"
	"os/exec"
	"strings"
)

type darwinSyslog struct {
	out    io.Writer
	source string
}

func (s *darwinSyslog) Write(data []byte) (int, error) {
	// Write to event log
	line := string(data)

	// Extract the message
	message := extractMessage(line)

	// Use different levels
	priority := "daemon.info"
	if strings.Contains(line, "[ERROR]") {
		priority = "daemon.err"
	} else if strings.Contains(line, "[WARNING]") {
		priority = "daemon.warning"
	}

	// Write to system logger
	cmd := exec.Command("logger", "-p", priority, "-t", s.source, message)
	cmd.Run()

	return s.out.Write(data)
}

func (s *darwinSyslog) Close() error {
	return nil
}

func New(name string, out io.Writer) (Syslog, error) {
	return &darwinSyslog{
		out:    out,
		source: name,
	}, nil
}
