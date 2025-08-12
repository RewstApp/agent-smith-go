//go:build linux

package syslog

import (
	"io"
)

type linuxSyslog struct {
	out io.Writer
}

func (s *linuxSyslog) Write(data []byte) (int, error) {
	return s.out.Write(data)
}

func (s *linuxSyslog) Close() error {
	return nil
}

func New(name string, out io.Writer) (Syslog, error) {
	// TODO: Implement
	return &linuxSyslog{
		out: out,
	}, nil
}
