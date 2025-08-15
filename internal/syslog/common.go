package syslog

import "strings"

type Syslog interface {
	Write(p []byte) (int, error)
	Close() error
}

func extractMessage(line string) string {
	start := strings.Index(line, "]") + 2
	return line[start:]
}
