//go:build darwin

package syslog

import (
	"io"
	"os/exec"
	"strings"
)

type commandRunner interface {
	Run(priority, source, message string) error
}

type loggerCommandRunner struct{}

func (r *loggerCommandRunner) Run(priority, source, message string) error {
	return exec.Command("logger", "-p", priority, "-t", source, message).Run()
}

type darwinSyslog struct {
	out    io.Writer
	source string
	runner commandRunner
}

func (s *darwinSyslog) Write(data []byte) (int, error) {
	line := string(data)
	message := extractMessage(line)

	priority := "daemon.info"
	if strings.Contains(line, "[ERROR]") {
		priority = "daemon.err"
	} else if strings.Contains(line, "[WARNING]") {
		priority = "daemon.warning"
	}

	s.runner.Run(priority, s.source, message)

	return s.out.Write(data)
}

func (s *darwinSyslog) Close() error {
	return nil
}

func New(name string, out io.Writer) (Syslog, error) {
	return newWithRunner(name, out, &loggerCommandRunner{}), nil
}

func newWithRunner(name string, out io.Writer, runner commandRunner) Syslog {
	return &darwinSyslog{out: out, source: name, runner: runner}
}
