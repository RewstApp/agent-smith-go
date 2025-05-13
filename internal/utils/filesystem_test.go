package utils

import (
	"path/filepath"
	"testing"
)

func TestDirExists(t *testing.T) {
	tmpDir := t.TempDir()

	if !DirExists(tmpDir) {
		t.Errorf("expected true for existing directory %s", tmpDir)
	}

	nonExistentPath := filepath.Join(tmpDir, "does_not_exist")

	if DirExists(nonExistentPath) {
		t.Errorf("expected false for non-existent directory %s", nonExistentPath)
	}
}

func TestCreateFolderIfMissing(t *testing.T) {
	tmpDir := t.TempDir()
	newDir := filepath.Join(tmpDir, "new_folder")

	err := CreateFolderIfMissing(newDir)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !DirExists(newDir) {
		t.Errorf("expected the directory %s to be created", newDir)
	}

	err = CreateFolderIfMissing(newDir)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}
