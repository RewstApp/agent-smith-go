//go:build e2e

package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// staleAge is how far back the files that must be swept are dated: twice the
// 24 h threshold both sweeps apply. Ageing is the only thing simulated; it is
// exactly what a long-lived installation does by staying up.
const staleAge = 48 * time.Hour

// TestSweeps exercises the two startup sweeps against seeded directories: a
// stale file matching the agent's own temp-name pattern must go, a fresh one
// (a command or update still in flight) and a stale one under a foreign name
// (an operator's file) must stay (sc-103967, sc-106111).
func TestSweeps(t *testing.T) {
	h := New(t)
	h.Install()

	t.Run("StartupScriptSweep", func(t *testing.T) {
		dir := h.ScriptsDir()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}
		stale := filepath.Join(dir, "exec-900000001.ps1")
		fresh := filepath.Join(dir, "exec-900000002.ps1")
		foreign := filepath.Join(dir, "operator-script.ps1")
		seedFile(t, stale, []byte("# stale agent script (e2e)\n"), staleAge)
		seedFile(t, fresh, []byte("# fresh agent script (e2e)\n"), 0)
		seedFile(t, foreign, []byte("# operator script (e2e)\n"), staleAge)

		baseline := h.LogCount("Swept stale script files")
		h.RestartService()
		h.WaitForLogCount("Swept stale script files", baseline, 2*time.Minute)

		assertGone(t, stale)
		assertPresent(t, fresh)
		assertPresent(t, foreign)
	})

	t.Run("StartupInstallerSweep", func(t *testing.T) {
		dir := h.UpdatesDir()
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("creating %s: %v", dir, err)
		}
		// Sized in megabytes, as the leak this covers is: tens of megabytes
		// per update, not a count of files.
		payload := bytes.Repeat([]byte{0}, 4<<20)
		stale := filepath.Join(dir, "installer-900000001.bin")
		fresh := filepath.Join(dir, "installer-900000002.bin")
		foreign := filepath.Join(dir, "vendor-installer.bin")
		seedFile(t, stale, payload, staleAge)
		seedFile(t, fresh, payload, 0)
		seedFile(t, foreign, payload, staleAge)

		baseline := h.LogCount("Swept stale installer files")
		h.RestartService()
		h.WaitForLogCount("Swept stale installer files", baseline, 2*time.Minute)

		assertGone(t, stale)
		assertPresent(t, fresh)
		assertPresent(t, foreign)
	})
}

// seedFile writes content to path and, when age is non-zero, dates the file
// that far into the past.
func seedFile(t *testing.T, path string, content []byte, age time.Duration) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
	if age == 0 {
		return
	}
	then := time.Now().Add(-age)
	if err := os.Chtimes(path, then, then); err != nil {
		t.Fatalf("ageing %s: %v", path, err)
	}
}

func assertGone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Errorf("stale file was not swept: %s", path)
	} else if !os.IsNotExist(err) {
		t.Errorf("stat %s: %v", path, err)
	}
}

func assertPresent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("a file the sweep must not touch is gone: %s (%v)", path, err)
	}
}
