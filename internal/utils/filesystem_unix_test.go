//go:build !windows

package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureSecureDir_FixesPermissiveExistingDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "scripts")

	// Simulate a directory left behind (or pre-planted) with a permissive mode
	// rather than one EnsureSecureDir created.
	if err := os.MkdirAll(dir, 0o777); err != nil {
		t.Fatal(err)
	}

	if err := EnsureSecureDir(dir); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("expected directory to still exist: %v", err)
	}
	if got := info.Mode().Perm(); got != SecureDirMode {
		t.Errorf("expected mode to be corrected to %o, got %o", SecureDirMode, got)
	}
}
