package syslog

type Syslog interface {
	Write(p []byte) (int, error)
	Close() error
}
