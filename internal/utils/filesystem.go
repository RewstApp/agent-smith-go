package utils

import (
	"os"
)

const (
	DefaultFileMod           os.FileMode = 0o644
	DefaultExecutableFileMod os.FileMode = 0o755
	DefaultDirMod            os.FileMode = 0o755

	// SecureDirMode is enforced by EnsureSecureDir on directories that must not
	// be readable, writable, or traversable by any local account other than the
	// one the agent runs as (root/SYSTEM) — currently the command scripts
	// directory (see agent.GetScriptsDirectory). It is tighter than
	// DefaultDirMod: nothing but the agent itself ever needs to see inside.
	SecureDirMode os.FileMode = 0o700
)

type FileSystem interface {
	Executable() (string, error)
	ReadFile(name string) ([]byte, error)
	WriteFile(name string, data []byte, perm os.FileMode) error
	MkdirAll(path string) error
	RemoveAll(path string) error
	// Rename moves oldPath to newPath, replacing newPath if it exists. It is the
	// commit step of an atomic write: the caller writes a temporary file in the
	// destination directory and renames it into place, so a failed write never
	// leaves a truncated file behind.
	Rename(oldPath string, newPath string) error
	// Remove deletes a single file. It is used to clean up the temporary file of
	// an atomic write that could not be committed.
	Remove(name string) error
	// ExecutableInUse reports whether the file at name is currently held open as
	// a running image by some process. It is a real observation, not a guess: the
	// probe opens the file for writing, which a running executable refuses with a
	// sharing violation on Windows and with ETXTBSY on Linux and macOS.
	//
	// A file that does not exist is not in use. Any other failure to probe (a
	// restrictive ACL, an unreadable parent directory) is returned as an error so
	// the caller can decide what to do rather than silently reading as "free".
	ExecutableInUse(name string) (bool, error)
	// EnsureSecureDir creates path if it does not exist, or brings it in line
	// with SecureDirMode (and, on platforms with POSIX ownership, the agent's
	// own uid) if it does — every time it is called, not only on first
	// creation. Unlike MkdirAll, an existing directory is not silently trusted
	// as-is: a directory pre-planted by an unprivileged local user (or left
	// over in a location that used to be shared, world-writable temp) is
	// detected and corrected, or the call fails loud, rather than being reused
	// with whatever ownership/mode it happens to carry.
	EnsureSecureDir(path string) error
}

type defaultFileSystem struct{}

func (*defaultFileSystem) Executable() (string, error) {
	return os.Executable()
}

func (*defaultFileSystem) ReadFile(name string) ([]byte, error) {
	return os.ReadFile(name)
}

func (*defaultFileSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	return os.WriteFile(name, data, perm)
}

func (*defaultFileSystem) MkdirAll(path string) error {
	return os.MkdirAll(path, DefaultDirMod)
}

func (*defaultFileSystem) RemoveAll(path string) error {
	return os.RemoveAll(path)
}

func (*defaultFileSystem) Rename(oldPath string, newPath string) error {
	return os.Rename(oldPath, newPath)
}

func (*defaultFileSystem) Remove(name string) error {
	return os.Remove(name)
}

func (*defaultFileSystem) ExecutableInUse(name string) (bool, error) {
	return executableInUse(name)
}

func (*defaultFileSystem) EnsureSecureDir(path string) error {
	return EnsureSecureDir(path)
}

func NewFileSystem() FileSystem {
	return &defaultFileSystem{}
}
