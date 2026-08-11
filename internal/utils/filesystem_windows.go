//go:build windows

package utils

import (
	"errors"
	"os"
	"syscall"
)

// errSharingViolation is ERROR_SHARING_VIOLATION. Windows maps a running image
// to it: the loader keeps the executable open denying write sharing, so opening
// it for writing fails with this error for exactly as long as the process lives.
// It is the same error that made an in-place update fail after the fixed five
// second sleep, used here as the signal to wait on instead of the symptom.
const errSharingViolation = syscall.Errno(32)

// executableInUse probes whether name is currently running. A successful open
// means no process holds the image and the file can be replaced; a sharing
// violation means the old process is still alive.
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

	if errors.Is(err, errSharingViolation) {
		return true, nil
	}

	return false, err
}
