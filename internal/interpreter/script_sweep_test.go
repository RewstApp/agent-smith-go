package interpreter

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

// writeScriptFile creates a file in dir with the given modification age.
func writeScriptFile(t *testing.T, dir, name string, age time.Duration) string {
	t.Helper()

	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("echo hello"), utils.DefaultFileMod); err != nil {
		t.Fatalf("failed to write %s: %v", name, err)
	}

	modTime := time.Now().Add(-age)
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatalf("failed to set mtime on %s: %v", name, err)
	}
	return path
}

func newSweepLogger() (hclog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return hclog.New(&hclog.LoggerOptions{Output: &buf, Level: hclog.Debug}), &buf
}

func TestSweepStaleScripts_RemovesStaleFiles(t *testing.T) {
	dir := t.TempDir()
	stale := []string{"exec-123456789.ps1", "exec-987654321.ps1", "exec-1.ps1"}
	for _, name := range stale {
		writeScriptFile(t, dir, name, 48*time.Hour)
	}

	logger, buf := newSweepLogger()
	removed := SweepStaleScripts(dir, DefaultStaleScriptAge, logger)

	if removed != len(stale) {
		t.Errorf("expected %d files removed, got %d", len(stale), removed)
	}
	for _, name := range stale {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("expected %s to be removed, stat err = %v", name, err)
		}
	}
	if logs := buf.String(); !strings.Contains(logs, "Swept stale script files") {
		t.Errorf("expected a summary log line, got %q", logs)
	}
}

func TestSweepStaleScripts_KeepsRecentAndForeignFiles(t *testing.T) {
	dir := t.TempDir()

	// A script file younger than the threshold: it may belong to a command that is
	// still executing, so it must survive.
	recent := writeScriptFile(t, dir, "exec-111111111.ps1", time.Minute)

	// Old files that do not match the agent's own temp pattern belong to someone
	// else and must never be touched, however stale they are.
	foreign := []string{
		"exec-backup.ps1",       // non-numeric middle
		"exec-.ps1",             // empty middle
		"exec-123456789.ps1.gz", // wrong suffix
		"exec-123456789.sh",     // wrong suffix
		"prefix-exec-1.ps1",     // wrong prefix
		"unrelated.ps1",         // unrelated file
	}
	for _, name := range foreign {
		writeScriptFile(t, dir, name, 72*time.Hour)
	}

	// A stale directory that happens to match the pattern must also be left alone.
	staleDir := filepath.Join(dir, "exec-222222222.ps1.d")
	if err := os.Mkdir(staleDir, utils.DefaultDirMod); err != nil {
		t.Fatalf("failed to create dir: %v", err)
	}

	logger, _ := newSweepLogger()
	if removed := SweepStaleScripts(dir, DefaultStaleScriptAge, logger); removed != 0 {
		t.Errorf("expected no files removed, got %d", removed)
	}

	if _, err := os.Stat(recent); err != nil {
		t.Errorf("expected recent script file to survive: %v", err)
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

func TestSweepStaleScripts_MixedDirectory(t *testing.T) {
	dir := t.TempDir()
	writeScriptFile(t, dir, "exec-100000001.ps1", 25*time.Hour)
	survivor := writeScriptFile(t, dir, "exec-100000002.ps1", 23*time.Hour)

	logger, _ := newSweepLogger()
	if removed := SweepStaleScripts(dir, DefaultStaleScriptAge, logger); removed != 1 {
		t.Errorf("expected exactly 1 file removed, got %d", removed)
	}
	if _, err := os.Stat(survivor); err != nil {
		t.Errorf("expected file younger than the threshold to survive: %v", err)
	}
}

func TestSweepStaleScripts_MissingDirectoryIsNotAnError(t *testing.T) {
	logger, buf := newSweepLogger()

	removed := SweepStaleScripts(filepath.Join(t.TempDir(), "does-not-exist"), time.Hour, logger)

	if removed != 0 {
		t.Errorf("expected 0 files removed, got %d", removed)
	}
	if logs := buf.String(); strings.Contains(logs, "[ERROR]") {
		t.Errorf("a missing scripts directory must not log an error, got %q", logs)
	}
}

func TestSweepStaleScripts_ReadDirFailureIsLoggedNotFatal(t *testing.T) {
	// A path that is a regular file rather than a directory. What ReadDir reports
	// for that is platform-specific — ENOTDIR on Unix, but a not-exist-flavoured
	// error or simply zero entries on Windows, where the directory read runs
	// against an open file handle — so the log expectation is derived from what
	// this platform actually reports. What must hold everywhere is that the sweep
	// removes nothing and does not fail; a reported failure is logged, and an
	// absent or not-exist error stays silent like the fresh-install case.
	path := writeScriptFile(t, t.TempDir(), "not-a-dir", 0)

	logger, buf := newSweepLogger()
	if removed := SweepStaleScripts(path, time.Hour, logger); removed != 0 {
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

	if !strings.Contains(logs, "Failed to read scripts directory") {
		t.Errorf("expected the read failure (%v) to be logged, got %q", readErr, logs)
	}
}

func TestSweepStaleScripts_DefaultsMaxAgeWhenNonPositive(t *testing.T) {
	dir := t.TempDir()
	// Younger than DefaultStaleScriptAge: a zero maxAge must fall back to the
	// default rather than deleting everything it finds.
	survivor := writeScriptFile(t, dir, "exec-333333333.ps1", time.Hour)
	writeScriptFile(t, dir, "exec-444444444.ps1", 48*time.Hour)

	logger, _ := newSweepLogger()
	if removed := SweepStaleScripts(dir, 0, logger); removed != 1 {
		t.Errorf("expected exactly 1 file removed, got %d", removed)
	}
	if _, err := os.Stat(survivor); err != nil {
		t.Errorf("expected recent file to survive with a defaulted max age: %v", err)
	}
}

// The sweep must match exactly what the executor creates: build a real temp file
// through the same os.CreateTemp pattern and assert the matcher accepts it.
func TestIsScriptFile_MatchesExecutorTempPattern(t *testing.T) {
	dir := t.TempDir()
	f, err := os.CreateTemp(dir, scriptTempPattern)
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	name := filepath.Base(f.Name())
	if err := f.Close(); err != nil {
		t.Fatalf("failed to close temp file: %v", err)
	}

	if !isScriptFile(name) {
		t.Errorf("isScriptFile(%q) = false, want true for an executor-created name", name)
	}
}
