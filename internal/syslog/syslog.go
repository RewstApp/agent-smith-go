package syslog

import "strings"

type Syslog interface {
	Write(p []byte) (int, error)
	Close() error
}

// logLevel is the severity a formatted log line carries, recovered from the
// level bracket hclog writes into it. Every platform maps this onto its own
// sink (syslog priorities on Linux/macOS, event log severities on Windows);
// the classification itself lives here so the three writers cannot drift apart
// again - each used to match the bracket text itself, and all three looked for
// "[WARNING]", which hclog never emits (it writes "[WARN] "), so warnings were
// silently logged as informational on every platform.
type logLevel int

const (
	levelInfo logLevel = iota
	levelWarning
	levelError
)

func levelForLine(line string) logLevel {
	switch {
	case strings.Contains(line, "[ERROR]"):
		return levelError
	// "[WARNING]" is not what hclog emits, but it costs nothing to keep
	// honouring the spelling this code has always looked for.
	case strings.Contains(line, "[WARN]"), strings.Contains(line, "[WARNING]"):
		return levelWarning
	default:
		return levelInfo
	}
}

func extractMessage(line string) string {
	idx := strings.Index(line, "]")
	if idx < 0 || idx+2 > len(line) {
		return line
	}
	return line[idx+2:]
}
