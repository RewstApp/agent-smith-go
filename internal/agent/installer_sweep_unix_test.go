//go:build !windows

package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/utils"
)

// A file the sweep cannot remove must be logged and skipped, not fail the sweep
// or the agent start that runs it. Removal is denied by making the containing
// directory read-only, which is the portable Unix way to refuse an unlink;
// Windows denies removal through sharing violations instead and is covered by
// the running-installer case in the integration workflow.
func TestSweepStaleInstallers_UnremovableFileIsLoggedAndSkipped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions, so removal cannot be made to fail this way")
	}

	parent := t.TempDir()
	dir := filepath.Join(parent, "updates")
	if err := os.Mkdir(dir, utils.DefaultDirMod); err != nil {
		t.Fatalf("failed to create updates directory: %v", err)
	}

	stuck := writeInstallerFile(t, dir, "installer-777777777.bin", 48*time.Hour)

	// Read and traverse but not write: the sweep can list and stat the entry and
	// still be refused the unlink.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("failed to make the directory read-only: %v", err)
	}
	t.Cleanup(func() {
		// Restore write permission so t.TempDir cleanup can remove the tree.
		_ = os.Chmod(dir, utils.DefaultDirMod)
	})

	logger, buf := newInstallerSweepLogger()
	removed := SweepStaleInstallers(dir, DefaultStaleInstallerAge, logger)

	if removed != 0 {
		t.Errorf("expected 0 files removed, got %d", removed)
	}
	if _, err := os.Stat(stuck); err != nil {
		t.Errorf("expected the unremovable file to still be there: %v", err)
	}

	logs := buf.String()
	if !strings.Contains(logs, "Failed to remove stale installer file") {
		t.Errorf("expected the removal failure to be logged, got %q", logs)
	}
	// A sweep that removed nothing must not claim it swept anything.
	if strings.Contains(logs, "Swept stale installer files") {
		t.Errorf("expected no summary line when nothing was removed, got %q", logs)
	}
}
