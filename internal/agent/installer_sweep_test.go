package agent

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/hashicorp/go-hclog"
)

// writeInstallerFile creates a file in dir with the given modification age,
// standing in for an installer binary a previous update left behind.
func writeInstallerFile(t *testing.T, dir, name string, age time.Duration) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("fake agent binary"), utils.DefaultFileMod); err != nil {
		t.Fatalf("failed to write %s: %v", name, err)
	}

	modTime := time.Now().Add(-age)
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("failed to set mtime on %s: %v", name, err)
	}
	return path
}

func newInstallerSweepLogger() (hclog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return hclog.New(&hclog.LoggerOptions{Output: &buf, Level: hclog.Debug}), &buf
}

func TestSweepStaleInstallers_RemovesStaleFiles(t *testing.T) {
	dir := t.TempDir()
	stale := []string{"installer-123456789.bin", "installer-987654321.bin", "installer-1.bin"}
	for _, name := range stale {
		writeInstallerFile(t, dir, name, 48*time.Hour)
	}

	logger, buf := newInstallerSweepLogger()
	removed := SweepStaleInstallers(dir, DefaultStaleInstallerAge, logger)

	if removed != len(stale) {
		t.Errorf("expected %d files removed, got %d", len(stale), removed)
	}
	for _, name := range stale {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed, stat err = %v", name, err)
		}
	}

	logs := buf.String()
	// The summary line carries the count and the directory, so an operator
	// reading the log can tell how much was reclaimed and from where.
	if !strings.Contains(logs, "Swept stale installer files") {
		t.Errorf("expected a summary log line, got %q", logs)
	}
	if !strings.Contains(logs, "removed=3") || !strings.Contains(logs, dir) {
		t.Errorf("expected the summary to name the count and directory, got %q", logs)
	}
	// Individual removals stay at Debug so a normal Info-level log is one line.
	if !strings.Contains(logs, "[DEBUG]") ||
		!strings.Contains(logs, "Removed stale installer file") {
		t.Errorf("expected per-file removals to be logged at debug, got %q", logs)
	}
}

func TestSweepStaleInstallers_KeepsRecentAndForeignFiles(t *testing.T) {
	dir := t.TempDir()

	// An installer younger than the threshold: it may be the one currently being
	// executed by the update that just restarted this agent, so it must survive.
	recent := writeInstallerFile(t, dir, "installer-111111111.bin", time.Minute)

	// Old files that do not match the agent's own temp pattern belong to someone
	// else and must never be touched, however stale they are. This matters most
	// in the legacy shared temp directory, which is full of other programs' work.
	foreign := []string{
		"installer-setup.bin",       // non-numeric middle
		"installer-.bin",            // empty middle
		"installer-123456789.bin.1", // wrong suffix
		"installer-123456789.exe",   // wrong suffix
		"prefix-installer-1.bin",    // wrong prefix
		"installer.bin",             // prefix without the separator
		"unrelated.bin",             // unrelated file
	}
	for _, name := range foreign {
		writeInstallerFile(t, dir, name, 72*time.Hour)
	}

	// A stale directory that happens to match the pattern must also be left alone.
	staleDir := filepath.Join(dir, "installer-222222222.bin.d")
	if err := os.Mkdir(staleDir, utils.DefaultDirMod); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	logger, _ := newInstallerSweepLogger()
	if removed := SweepStaleInstallers(dir, DefaultStaleInstallerAge, logger); removed != 0 {
		t.Errorf("expected no files removed, got %d", removed)
	}

	if _, err := os.Stat(recent); err != nil {
		t.Errorf("expected recent installer file to survive: %v", err)
	}
	for _, name := range foreign {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("expected %s to survive the sweep: %v", name, err)
		}
	}
	if _, err := os.Stat(staleDir); err != nil {
		t.Errorf("expected directory to survive the sweep: %v", err)
	}
}

func TestSweepStaleInstallers_MixedDirectory(t *testing.T) {
	dir := t.TempDir()
	writeInstallerFile(t, dir, "installer-100000001.bin", 25*time.Hour)
	survivor := writeInstallerFile(t, dir, "installer-100000002.bin", 23*time.Hour)

	logger, _ := newInstallerSweepLogger()
	if removed := SweepStaleInstallers(dir, DefaultStaleInstallerAge, logger); removed != 1 {
		t.Errorf("expected exactly 1 file removed, got %d", removed)
	}
	if _, err := os.Stat(survivor); err != nil {
		t.Errorf("expected file younger than the threshold to survive: %v", err)
	}
}

// A symlink carrying the installer name must not be followed: removing it would
// delete a link whose target belongs to someone else. The threshold is set below
// the link's own age so the regular-file check is the only thing that can save
// it — otherwise the test would pass on the age check and prove nothing. The
// control file confirms the sweep really was active at that threshold.
func TestSweepStaleInstallers_DoesNotFollowSymlinks(t *testing.T) {
	dir := t.TempDir()
	targetDir := t.TempDir()

	target := writeInstallerFile(t, targetDir, "real-target.bin", time.Hour)
	link := filepath.Join(dir, "installer-555555555.bin")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks are not available to this test process: %v", err)
	}

	control := writeInstallerFile(t, dir, "installer-666666666.bin", time.Hour)

	// Every entry in the directory is now older than the cutoff.
	time.Sleep(10 * time.Millisecond)

	logger, _ := newInstallerSweepLogger()
	if removed := SweepStaleInstallers(dir, time.Millisecond, logger); removed != 1 {
		t.Errorf("expected exactly the control file to be removed, got %d", removed)
	}
	if _, err := os.Stat(control); !os.IsNotExist(err) {
		t.Fatalf("the sweep did not run at this threshold; the symlink assertion is vacuous")
	}
	if _, err := os.Lstat(link); err != nil {
		t.Errorf("expected the symlink to survive the sweep: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("expected the symlink target to survive the sweep: %v", err)
	}
}

func TestSweepStaleInstallers_MissingDirectoryIsNotAnError(t *testing.T) {
	logger, buf := newInstallerSweepLogger()

	removed := SweepStaleInstallers(filepath.Join(t.TempDir(), "does-not-exist"), time.Hour, logger)

	if removed != 0 {
		t.Errorf("expected 0 files removed, got %d", removed)
	}
	if logs := buf.String(); strings.Contains(logs, "[ERROR]") {
		t.Errorf("a missing updates directory must not log an error, got %q", logs)
	}
}

func TestSweepStaleInstallers_ReadDirFailureIsLoggedNotFatal(t *testing.T) {
	// A path that is a regular file rather than a directory. What ReadDir reports
	// for that is platform-specific — ENOTDIR on Unix, but a not-exist-flavoured
	// error or simply zero entries on Windows, where the directory read runs
	// against an open file handle — so the log expectation is derived from what
	// this platform actually reports. What must hold everywhere is that the sweep
	// removes nothing and does not fail; a reported failure is logged, and an
	// absent or not-exist error stays silent like the fresh-install case.
	path := writeInstallerFile(t, t.TempDir(), "not-a-dir", 0)

	logger, buf := newInstallerSweepLogger()
	if removed := SweepStaleInstallers(path, time.Hour, logger); removed != 0 {
		t.Errorf("expected 0 files removed, got %d", removed)
	}

	logs := buf.String()
	_, readErr := os.ReadDir(path)
	if readErr == nil || os.IsNotExist(readErr) {
		if strings.Contains(logs, "[ERROR]") {
			t.Errorf("expected no error log when ReadDir reports %v, got %q", readErr, logs)
		}
		return
	}

	if !strings.Contains(logs, "Failed to read updates directory") {
		t.Errorf("expected the read failure (%v) to be logged, got %q", readErr, logs)
	}
}

func TestSweepStaleInstallers_DefaultsMaxAgeWhenNonPositive(t *testing.T) {
	dir := t.TempDir()
	// Younger than DefaultStaleInstallerAge: a zero maxAge must fall back to the
	// default rather than deleting everything it finds.
	survivor := writeInstallerFile(t, dir, "installer-333333333.bin", time.Hour)
	writeInstallerFile(t, dir, "installer-444444444.bin", 48*time.Hour)

	logger, _ := newInstallerSweepLogger()
	if removed := SweepStaleInstallers(dir, 0, logger); removed != 1 {
		t.Errorf("expected exactly 1 file removed, got %d", removed)
	}
	if _, err := os.Stat(survivor); err != nil {
		t.Errorf("expected recent file to survive with a defaulted max age: %v", err)
	}
}

// The sweep must match exactly what the updater downloads: build a real temp
// file through the same os.CreateTemp pattern and assert the matcher accepts it.
func TestIsInstallerFile_MatchesUpdaterTempPattern(t *testing.T) {
	dir := t.TempDir()
	f, err := os.CreateTemp(dir, installerTempPattern)
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	name := filepath.Base(f.Name())
	if err := f.Close(); err != nil {
		t.Fatalf("failed to close temp file: %v", err)
	}

	if !isInstallerFile(name) {
		t.Errorf("isInstallerFile(%q) = false, want true for an updater-created name", name)
	}
}

// The download location and the swept location are derived from the same helper,
// so an installer can never land somewhere the sweep does not look.
func TestGetUpdatesDirectory_IsUnderTheOrgDataDirectory(t *testing.T) {
	const orgId = "test-org"

	updatesDir := GetUpdatesDirectory(orgId)
	dataDir := GetDataDirectory(orgId)

	if parent := filepath.Dir(updatesDir); parent != dataDir {
		t.Errorf("expected the updates directory under %s, got %s", dataDir, updatesDir)
	}
	if updatesDir == dataDir {
		t.Error("the updates directory must be a subdirectory, not the data directory itself")
	}
	if updatesDir == os.TempDir() {
		t.Error("the updates directory must not be the shared system temp directory")
	}
}
