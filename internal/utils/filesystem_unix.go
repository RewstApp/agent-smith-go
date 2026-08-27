//go:build !windows

package utils

import (
	"errors"
	"fmt"
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

// EnsureSecureDir creates path with SecureDirMode if it does not exist. If it
// does exist, it re-asserts SecureDirMode and the current process's effective
// uid on every call rather than trusting whatever ownership/mode the
// directory happens to carry: a bare os.MkdirAll is a no-op against an
// already-existing directory, which is exactly how a shared, world-writable
// temp directory let an unprivileged local user pre-create the command
// scripts directory with permissive ownership and have the agent silently
// reuse it (sc-108848).
//
// A symlink or non-directory entry at path is refused outright rather than
// followed or replaced — MkdirAll never produces one, so its presence means
// something else put it there. An ownership mismatch is corrected via Chown
// rather than treated as fatal, since the agent runs privileged and is about
// to write into and execute from this directory anyway; either Chown or
// Chmod failing is returned as a loud error instead of proceeding with the
// wrong ownership/mode.
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

	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("failed to read ownership of %s", path)
	}

	euid := uint32(os.Geteuid())
	if stat.Uid != euid {
		if err := os.Chown(path, int(euid), os.Getegid()); err != nil {
			return fmt.Errorf(
				"refusing to use %s: owned by uid %d instead of %d, and could not reclaim it: %w",
				path, stat.Uid, euid, err,
			)
		}
	}

	if fi.Mode().Perm() != SecureDirMode {
		if err := os.Chmod(path, SecureDirMode); err != nil {
			return fmt.Errorf("failed to fix permissions on %s: %w", path, err)
		}
	}

	return nil
}

// EnsureSecureFile brings path in line with SecureFileMode and the current
// process's effective uid, the file-level counterpart to EnsureSecureDir. It
// is how an installation's config file — written world-readable (0o644)
// before this hardening exposed the device's Azure IoT Hub SharedAccessKey
// and GitHub token to any local account — gets corrected on the next update
// or service start instead of only protecting files written after the fix
// (sc-108849).
//
// A path that does not exist is not an error: nothing has been written yet,
// and the write path (writeFileAtomic) already writes with SecureFileMode. A
// symlink is refused rather than followed, for the same reason
// EnsureSecureDir refuses one: Chmod/Chown follow symlinks, so silently
// applying them here would act on whatever an attacker's symlink points at
// instead of the config file itself.
func EnsureSecureFile(path string) error {
	fi, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to stat %s: %w", path, err)
	}

	if fi.Mode()&os.ModeSymlink != 0 || !fi.Mode().IsRegular() {
		return fmt.Errorf("refusing to use %s: exists and is not a plain file", path)
	}

	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("failed to read ownership of %s", path)
	}

	euid := uint32(os.Geteuid())
	if stat.Uid != euid {
		if err := os.Chown(path, int(euid), os.Getegid()); err != nil {
			return fmt.Errorf(
				"refusing to use %s: owned by uid %d instead of %d, and could not reclaim it: %w",
				path, stat.Uid, euid, err,
			)
		}
	}

	if fi.Mode().Perm() != SecureFileMode {
		if err := os.Chmod(path, SecureFileMode); err != nil {
			return fmt.Errorf("failed to fix permissions on %s: %w", path, err)
		}
	}

	return nil
}

// SecureDataDirectoryACL is a no-op on Linux/macOS: EnsureSecureDir and
// EnsureSecureFile already enforce SecureDirMode/SecureFileMode via POSIX
// mode/ownership bits on this platform, which is what the equivalent
// Windows ACL lockdown (filesystem_windows.go) exists to provide where those
// bits don't apply. It exists here only so callers in cmd/agent_smith can
// invoke it unconditionally without a build tag of their own.
func SecureDataDirectoryACL(path string, extraAccount string) error {
	return nil
}
