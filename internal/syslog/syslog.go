package syslog

import "strings"

type Syslog interface {
	Write(p []byte) (int, error)
	Close() error
}

func extractMessage(line string) string {
	idx := strings.Index(line, "]")
	if idx < 0 || idx+2 > len(line) {
		return line
	}
	return line[idx+2:]
}
