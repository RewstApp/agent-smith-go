package utils

import (
	"fmt"
	"io"
	"time"

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

// HclogWarnLine formats a synthetic WARN entry exactly the way hclog formats its
// own plain-text lines - same timestamp format, same level padding - so a
// diagnostic that a writer inserts into the log file on its own behalf (the
// syslog forwarder reporting a forwarding outage, the rotating file reporting a
// rotation failure) parses like every other line for levelForLine and for log
// tooling. Keeping the shape in one place means a change to it cannot leave one
// writer's synthetic lines desynchronised from the other's.
func HclogWarnLine(now time.Time, source, message string) string {
	return fmt.Sprintf("%s [WARN]  %s: %s\n", now.Format(hclog.TimeFormat), source, message)
}
