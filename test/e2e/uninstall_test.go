//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestUninstall covers the uninstall path that needs a fixture: a directory
// the uninstall cannot remove (sc-108857). An open handle without
// FILE_SHARE_DELETE makes the unlink itself fail on Windows - an AV scanner
// mid-scan, a stale handle from a process that touched the installation - and
// the uninstall must remove the other directories and name the one it could
// not, rather than stop at the first failure with the service already gone.
// Windows only: on Linux and macOS an open handle does not block unlink, so
// there is nothing to fix there; the platform-independent half (every directory
// attempted, every failure reported) is covered by unit tests.
func TestUninstall(t *testing.T) {
	t.Run("ContinuesPastLockedDirectory", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("an open handle blocks a delete only on Windows")
		}
		h := New(t)
		h.Install()

		locked := h.ProgramDir()
		lockFile := filepath.Join(locked, "av-scan-in-progress.tmp")
		// Never the agent executable: holding that open would make the exit
		// wait conclude the old agent is still running and abort before
		// removing anything, which is the wedged-service scenario.
		held := []byte("held open by the e2e fixture")
		if err := os.WriteFile(lockFile, held, 0o644); err != nil {
			t.Fatalf("creating the lock file: %v", err)
		}
		handle, err := os.Open(lockFile)
		if err != nil {
			t.Fatalf("holding the lock file open: %v", err)
		}
		t.Cleanup(func() {
			_ = handle.Close()
			_ = os.RemoveAll(locked)
		})
		if err := os.Remove(lockFile); err == nil {
			t.Fatalf("the lock file was deletable while the handle was held: %s", lockFile)
		}
		removed := []string{h.DataDir(), h.ScriptsDir()}
		if err := os.MkdirAll(h.ScriptsDir(), 0o700); err != nil {
			t.Fatalf("creating %s: %v", h.ScriptsDir(), err)
		}

		out, _, _ := h.Run(h.Binary, "--uninstall", "--org-id", h.OrgID)
		h.MarkUninstalled()
		t.Logf("---- uninstaller output ----\n%s", out)

		assertContainsAll(
			t,
			out,
			"Service deleted",
			"Uninstall incomplete; some directories could not be removed",
		)
		assertContainsNone(t, out, "Uninstall completed")
		if h.ServiceExists() {
			t.Error("the service registration survived the uninstall")
		}
		summary := ""
		for _, ln := range strings.Split(out, "\n") {
			if strings.Contains(ln, "Uninstall incomplete; some directories could not be removed") {
				summary = ln
				break
			}
		}
		if summary != "" {
			if !strings.Contains(summary, locked) {
				t.Errorf("the summary does not name the locked directory %s: %s", locked, summary)
			}
			attempted := fmt.Sprintf("attempted_count=%d", len(removed)+1)
			for _, pair := range []string{"failed_count=1", attempted} {
				if !strings.Contains(summary, pair) {
					t.Errorf("the summary does not report %s: %s", pair, summary)
				}
			}
		}
		for _, dir := range removed {
			if !strings.Contains(out, dir) {
				t.Errorf(
					"the uninstall never mentioned %s, so it was skipped after the failure",
					dir,
				)
			}
			if _, err := os.Stat(dir); err == nil {
				t.Errorf("directory survived the uninstall: %s", dir)
			}
		}
		if _, err := os.Stat(locked); err != nil {
			t.Errorf(
				"the locked directory was reported as failed but is gone: %s (%v)",
				locked,
				err,
			)
		}
	})
}
