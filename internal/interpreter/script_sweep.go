package interpreter

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/go-hclog"
)

const (
	// scriptFilePrefix and scriptFileSuffix are the fixed parts of the temp script
	// name pattern handed to os.CreateTemp in Execute ("exec-*.ps1"). They are
	// shared with the startup sweep so the pattern it matches can never drift from
	// the pattern the executor writes.
	scriptFilePrefix = "exec-"
	scriptFileSuffix = ".ps1"

	// scriptTempPattern is the os.CreateTemp pattern for a command script file.
	scriptTempPattern = scriptFilePrefix + "*" + scriptFileSuffix

	// DefaultStaleScriptAge is how old an orphaned script file must be before the
	// startup sweep reclaims it. Execute always removes its own script file on
	// every exit path, so anything left behind belongs to a run that was killed
	// abruptly (SIGKILL, service force-stop, power loss, OOM). The threshold is
	// deliberately generous — commands are unbounded unless
	// command_timeout_seconds is configured, so a file this old cannot plausibly
	// belong to a command another agent process is still executing.
	DefaultStaleScriptAge = 24 * time.Hour
)

// SweepStaleScripts removes orphaned command script files left in dir by agent
// runs that were terminated before their deferred cleanup could run. Without it
// exec-*.ps1 files accumulate for the lifetime of the installation, consuming
// disk and leaving script contents on disk indefinitely.
//
// The sweep is intentionally conservative: it only considers regular files whose
// name matches the exact pattern Execute creates (see isScriptFile) and whose
// modification time is older than maxAge, so it can never delete a file created
// by another process or a script belonging to a command still in flight. Every
// failure — an unreadable directory, an unremovable file — is logged and
// skipped; the sweep never returns an error, because housekeeping must not block
// the agent from starting.
//
// It returns the number of files removed.
func SweepStaleScripts(dir string, maxAge time.Duration, logger hclog.Logger) int {
	if maxAge <= 0 {
		maxAge = DefaultStaleScriptAge
	}

	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		// A missing scripts directory is the normal state of a fresh install.
		if !os.IsNotExist(err) {
			logger.Error("Failed to read scripts directory for sweep", "dir", dir, "error", err)
		}
		return 0
	}

	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for _, de := range dirEntries {
		if de.IsDir() || !isScriptFile(de.Name()) {
			continue
		}

		info, err := de.Info()
		if err != nil {
			// The file vanished (or became unreadable) between ReadDir and here;
			// leave it for a later sweep rather than guessing at its age.
			if !os.IsNotExist(err) {
				logger.Error("Failed to stat stale script file", "file", de.Name(), "error", err)
			}
			continue
		}

		// Only regular files: a symlink or device node planted in the scripts
		// directory is not something this agent created and must not be followed.
		if !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
			continue
		}

		path := filepath.Join(dir, de.Name())
		if err := os.Remove(path); err != nil {
			if !os.IsNotExist(err) {
				logger.Error("Failed to remove stale script file", "file", path, "error", err)
			}
			continue
		}
		logger.Debug("Removed stale script file", "file", path, "modified", info.ModTime())
		removed++
	}

	if removed > 0 {
		logger.Info("Swept stale script files", "dir", dir, "removed", removed, "max_age", maxAge)
	}

	return removed
}

// isScriptFile reports whether name matches the temp script names produced by
// os.CreateTemp(dir, scriptTempPattern): the fixed prefix and suffix around a
// non-empty run of decimal digits. Requiring the random middle to be numeric
// keeps the sweep from touching similarly named files that this agent did not
// create (for example an operator's own "exec-backup.ps1").
func isScriptFile(name string) bool {
	if !strings.HasPrefix(name, scriptFilePrefix) || !strings.HasSuffix(name, scriptFileSuffix) {
		return false
	}

	middle := name[len(scriptFilePrefix) : len(name)-len(scriptFileSuffix)]
	if middle == "" {
		return false
	}
	for _, r := range middle {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
