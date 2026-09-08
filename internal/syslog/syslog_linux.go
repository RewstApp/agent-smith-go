//go:build linux

package syslog

// syslogMessage returns the text handed to `logger`. On Linux the -t flag is
// what tags the entry with the source, so the message is forwarded as-is.
func syslogMessage(source, message string) string {
	return message
}
