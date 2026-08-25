//go:build windows

package utils

import (
	"errors"
	"fmt"
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

// EnsureSecureDir creates path if it does not exist. Windows has no POSIX
// mode/ownership bits to re-assert here, so an existing entry is only checked
// for being a plain directory; a symlink or non-directory entry at path is
// refused rather than followed or replaced, since MkdirAll never produces
// one. This is deliberately the only check on Windows: the command scripts
// directory (agent.GetScriptsDirectory) stays at its historical location on
// this platform rather than moving under the agent-owned data directory the
// way it did on Linux/macOS — see the comment on that function for why — so
// there is no ProgramData-inherited ACL to lean on here the way there would
// be if it had moved.
func EnsureSecureDir(path string) error {
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(path, SecureDirMode); err != nil {
			return fmt.Errorf("failed to create secure directory %s: %w", path, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to stat %s: %w", path, err)
	}

	if fi.Mode()&os.ModeSymlink != 0 || !fi.IsDir() {
		return fmt.Errorf("refusing to use %s: exists and is not a plain directory", path)
	}

	return nil
}
