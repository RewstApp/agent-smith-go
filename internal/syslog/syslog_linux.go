//go:build linux

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

type linuxSyslog struct {
	out    io.Writer
	source string
	runner commandRunner
}

func (s *linuxSyslog) Write(data []byte) (int, error) {
	line := string(data)
	message := extractMessage(line)

	priority := "daemon.info"
	if strings.Contains(line, "[ERROR]") {
		priority = "daemon.err"
	} else if strings.Contains(line, "[WARNING]") {
		priority = "daemon.warning"
	}

	err := s.runner.Run(priority, s.source, message)
	if err != nil {
		return 0, err
	}

	return s.out.Write(data)
}

func (s *linuxSyslog) Close() error {
	return nil
}

func New(name string, out io.Writer) (Syslog, error) {
	return newWithRunner(name, out, &loggerCommandRunner{}), nil
}

func EnsureSource(_ string) error { return nil }

func newWithRunner(name string, out io.Writer, runner commandRunner) Syslog {
	return &linuxSyslog{out: out, source: name, runner: runner}
}
