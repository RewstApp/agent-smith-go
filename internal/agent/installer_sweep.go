package agent

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/go-hclog"
)

const (
	// installerFilePrefix and installerFileSuffix are the fixed parts of the
	// downloaded installer name pattern handed to os.CreateTemp in Download
	// ("installer-*.bin"). They are shared with the startup sweep so the pattern
	// it matches can never drift from the pattern the updater writes.
	installerFilePrefix = "installer-"
	installerFileSuffix = ".bin"

	// installerTempPattern is the os.CreateTemp pattern for a downloaded
	// installer binary.
	installerTempPattern = installerFilePrefix + "*" + installerFileSuffix

	// DefaultStaleInstallerAge is how old a downloaded installer binary must be
	// before the startup sweep reclaims it.
	//
	// Unlike a command script file, a successful installer is *meant* to outlive
	// the agent that downloaded it: Update spawns it detached and returns, and
	// the installer then stops the service, replaces the agent executable and
	// starts it again. The process that would delete the file is therefore the
	// process being replaced, which is why the download path cannot clean up
	// after itself and why the sweep is deferred to the next agent start.
	//
	// The threshold has to be comfortably longer than the longest plausible
	// install, because the freshly started agent sweeps while the installer that
	// started it may still be finishing. An install that has to wait for the old
	// process to exit is bounded at two minutes, so a day is three orders of
	// magnitude of headroom, and it still bounds steady-state usage to a single
	// file: every successful update restarts the agent, so each start reclaims
	// the previous update's installer while leaving the current one alone. It
	// matches DefaultStaleScriptAge for the same reason - one obvious number for
	// "far older than anything still in use".
	DefaultStaleInstallerAge = 24 * time.Hour
)

// GetUpdatesDirectory returns the directory the auto-updater downloads installer
// binaries into: a subdirectory of the org's data directory.
//
// Downloads used to go to the shared system temp directory. Owning the
// directory buys three things. The sweep below only ever runs against a
// directory this agent created, so it cannot plausibly reach a file another
// program left in temp. The directory is created 0700, so a full agent binary is
// not left executable and world-readable for a day at a time. And on Linux
// endpoints that mount /tmp noexec - a common hardening baseline - executing the
// downloaded installer works at all, which it did not when the download landed
// in temp. Uninstall already removes the data directory wholesale, so nothing is
// left behind by the move.
func GetUpdatesDirectory(orgId string) string {
	return filepath.Join(GetDataDirectory(orgId), "updates")
}

// SweepStaleInstallers removes downloaded installer binaries left in dir by
// previous update cycles. Download deliberately keeps the file it created so the
// installer can be executed, and nothing downstream removes it — Update starts
// the process detached and returns — so without this sweep one full agent binary
// (tens of megabytes) accumulates per update for the lifetime of the
// installation, until the volume it sits on fills and takes the endpoint's
// updates, command execution, and anything else needing scratch space with it.
//
// The sweep is intentionally conservative: it only considers regular files whose
// name matches the exact pattern Download creates (see isInstallerFile) and
// whose modification time is older than maxAge, so it can never delete a file
// created by another program or an installer that is still being executed. Every
// failure — an unreadable directory, an unremovable file — is logged and
// skipped; the sweep never returns an error, because housekeeping must not block
// the agent from starting.
//
// It returns the number of files removed.
func SweepStaleInstallers(dir string, maxAge time.Duration, logger hclog.Logger) int {
	if maxAge <= 0 {
		maxAge = DefaultStaleInstallerAge
	}

	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		// A missing updates directory is the normal state of an installation that
		// has not downloaded an update yet.
		if !os.IsNotExist(err) {
			logger.Error("Failed to read updates directory for sweep", "dir", dir, "error", err)
		}
		return 0
	}

	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for _, de := range dirEntries {
		if de.IsDir() || !isInstallerFile(de.Name()) {
			continue
		}

		info, err := de.Info()
		if err != nil {
			// The file vanished (or became unreadable) between ReadDir and here;
			// leave it for a later sweep rather than guessing at its age.
			if !os.IsNotExist(err) {
				logger.Error("Failed to stat stale installer file", "file", de.Name(), "error", err)
			}
			continue
		}

		// Only regular files: a symlink or device node carrying the installer name
		// is not something this agent created, and removing it would delete a link
		// whose target someone else owns.
		if !info.Mode().IsRegular() || !info.ModTime().Before(cutoff) {
			continue
		}

		path := filepath.Join(dir, de.Name())
		if err := os.Remove(path); err != nil {
			// A running installer still holds its own image open on Windows, so a
			// removal that loses the race with an unusually long install is an
			// expected outcome, not a fault: log it and let the next start retry.
			if !os.IsNotExist(err) {
				logger.Error("Failed to remove stale installer file", "file", path, "error", err)
			}
			continue
		}
		logger.Debug(
			"Removed stale installer file",
			"file", path,
			"size", info.Size(),
			"modified", info.ModTime(),
		)
		removed++
	}

	if removed > 0 {
		logger.Info(
			"Swept stale installer files",
			"dir", dir,
			"removed", removed,
			"max_age", maxAge,
		)
	}

	return removed
}

// isInstallerFile reports whether name matches the downloaded installer names
// produced by os.CreateTemp(dir, installerTempPattern): the fixed prefix and
// suffix around a non-empty run of decimal digits. Requiring the random middle
// to be numeric keeps the sweep from touching similarly named files this agent
// did not create — which matters most in the legacy shared temp directory, where
// files this agent has never written are the overwhelming majority.
func isInstallerFile(name string) bool {
	if !strings.HasPrefix(name, installerFilePrefix) ||
		!strings.HasSuffix(name, installerFileSuffix) {
		return false
	}

	middle := name[len(installerFilePrefix) : len(name)-len(installerFileSuffix)]
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
