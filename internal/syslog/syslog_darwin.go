//go:build darwin

package syslog

import (
	"io"
)

type darwinSyslog struct {
	out io.Writer
}

func (s *darwinSyslog) Write(data []byte) (int, error) {
	return s.out.Write(data)
}

func (s *darwinSyslog) Close() error {
	return nil
}

func New(name string, out io.Writer) (Syslog, error) {
	// TODO: Implement
	return &darwinSyslog{
		out: out,
	}, nil
}
