//go:build !windows

package main

import (
	"io"
	"os"
)

// Open opens the log file for reading. On Unix a plain open is enough: renaming
// a file another descriptor has open is always permitted, so the agent's log
// rotation is never blocked by a running viewer and logRotated detects the
// swap by inode.
func (o *osLogFileOpener) Open(name string) (io.ReadCloser, error) {
	return os.Open(name) // #nosec G304 - path comes from internal config
}
