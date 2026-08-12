package utils

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/shirou/gopsutil/v4/process"
)

// ProcessRunningFromExecutable reports whether any process other than the caller
// is currently executing the binary at path.
//
// This is the portable half of "has the old agent really exited". Windows and
// Linux also refuse to open a running image for writing, but macOS does not, so
// on macOS this is the only direct observation of the old process available —
// and everywhere it catches a process that is still winding down after its
// service manager has already stopped reporting the service as active.
//
// The caller's own process is excluded so an operator running the installed
// binary in place is not mistaken for the service it is updating. Processes that
// cannot be inspected (they exited mid-scan, or belong to another user) are
// skipped rather than failing the scan: they are not the stopped agent, whose
// executable path the caller already knows.
func ProcessRunningFromExecutable(path string) (bool, error) {
	processes, err := process.Processes()
	if err != nil {
		return false, err
	}

	self := int32(os.Getpid())
	targets := executablePathAliases(path)

	for _, proc := range processes {
		if proc.Pid == self {
			continue
		}

		executable, err := proc.Exe()
		if err != nil {
			continue
		}

		if targets[normalizeExecutablePath(executable)] {
			return true, nil
		}
	}

	return false, nil
}

// executablePathAliases returns every spelling of path a process might be
// reported under. Operating systems report a process's executable as the fully
// resolved path (macOS answers /private/var for a /var path, Linux resolves
// /proc/<pid>/exe), so the caller's own spelling alone would miss the match.
func executablePathAliases(path string) map[string]bool {
	aliases := map[string]bool{normalizeExecutablePath(path): true}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		aliases[normalizeExecutablePath(resolved)] = true
	}
	return aliases
}

// normalizeExecutablePath puts two executable paths into a form that can be
// compared: cleaned, and case-folded on Windows where paths are not case
// sensitive.
func normalizeExecutablePath(path string) string {
	cleaned := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(cleaned)
	}
	return cleaned
}
