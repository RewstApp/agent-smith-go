//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestOsLogFileOpener_DoesNotBlockRename is the direct proof for the
// FILE_SHARE_DELETE opener: while the viewer holds the log open, the agent's
// rotation (a rename of that file) must succeed. The contrast with a plain
// os.Open is logged rather than asserted, so this test keeps passing if a
// future Go release adds the flag itself.
func TestOsLogFileOpener_DoesNotBlockRename(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	if err := os.WriteFile(path, []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	rc, err := (&osLogFileOpener{}).Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rc.Close() }()

	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatalf("rename blocked while the viewer holds the file open: %v", err)
	}

	// Contrast, for the record.
	plainPath := filepath.Join(t.TempDir(), "plain.log")
	if err := os.WriteFile(plainPath, []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(plainPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if err := os.Rename(plainPath, plainPath+".1"); err != nil {
		t.Logf("as expected, a plain os.Open handle blocks the rename: %v", err)
	} else {
		t.Log(
			"a plain os.Open handle no longer blocks the rename on this Go/Windows; " +
				"the custom opener is now redundant",
		)
	}
}
