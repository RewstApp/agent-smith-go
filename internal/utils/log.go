package utils

import (
	"io"

	"github.com/hashicorp/go-hclog"
)

type LoggingLevel string

const (
	Trace   LoggingLevel = "trace"
	Debug   LoggingLevel = "debug"
	Info    LoggingLevel = "info"
	Warn    LoggingLevel = "warn"
	Error   LoggingLevel = "error"
	Off     LoggingLevel = "off"
	Default LoggingLevel = ""
)

func ConfigureLogger(prefix string, writer io.Writer, level LoggingLevel) hclog.Logger {
	return hclog.New(&hclog.LoggerOptions{
		Name:   prefix,
		Level:  hclog.LevelFromString(string(level)),
		Output: writer,
	})
}
