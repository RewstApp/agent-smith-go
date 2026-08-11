//go:build !windows

package utils

import (
	"errors"
	"os"
	"syscall"
)

// executableInUse probes whether name is currently running. Linux and macOS both
// refuse to open a running image for writing with ETXTBSY ("text file busy"),
// which is a direct statement from the kernel that the process is still alive —
// unlike a timer, it cannot report "gone" early.
//
// The file is opened without O_TRUNC and closed immediately, so a successful
// probe leaves its contents untouched.
func executableInUse(name string) (bool, error) {
	file, err := os.OpenFile(name, os.O_WRONLY, 0)
	if err == nil {
		return false, file.Close()
	}

	if errors.Is(err, os.ErrNotExist) {
		// Nothing installed at that path yet: no process can be holding it.
		return false, nil
	}

	if errors.Is(err, syscall.ETXTBSY) {
		return true, nil
	}

	return false, err
}
