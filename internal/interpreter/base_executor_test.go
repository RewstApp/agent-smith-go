package interpreter

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyScriptUnchanged_MatchingContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exec-1.ps1")
	content := []byte("echo hello")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := verifyScriptUnchanged(path, content); err != nil {
		t.Errorf("expected no error for unchanged content, got %v", err)
	}
}

func TestVerifyScriptUnchanged_TamperedContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "exec-2.ps1")
	if err := os.WriteFile(path, []byte("echo hello"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Simulate a swap in the write-close-reopen window: the file on disk no
	// longer matches what Execute believes it wrote.
	if err := verifyScriptUnchanged(path, []byte("echo attacker-controlled")); err == nil {
		t.Error("expected an error when on-disk content differs from what was written, got nil")
	}
}

func TestVerifyScriptUnchanged_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.ps1")

	if err := verifyScriptUnchanged(path, []byte("echo hello")); err == nil {
		t.Error("expected an error when the script file cannot be read, got nil")
	}
}
