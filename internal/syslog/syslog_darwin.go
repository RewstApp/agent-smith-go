//go:build darwin

package syslog

// syslogMessage returns the text handed to `logger`. macOS' unified logging
// does not surface the -t tag the way syslogd on Linux does, so the source is
// repeated in the message body to keep entries attributable.
func syslogMessage(source, message string) string {
	return source + ": " + message
}
