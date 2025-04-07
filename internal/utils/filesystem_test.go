package utils

import (
	"path/filepath"
	"testing"
)

func TestDirExists(t *testing.T) {
	// Create a temporary directory
	tmpDir := t.TempDir()

	if !DirExists(tmpDir) {
		t.Errorf("Expected DirExists to return true for existing directory: %s", tmpDir)
	}

	// Use a non-existent path
	nonExistentPath := filepath.Join(tmpDir, "does_not_exist")

	if DirExists(nonExistentPath) {
		t.Errorf("Expected DirExists to return false for non-existent directory: %s", nonExistentPath)
	}
}

func TestCreateFolderIfMissing(t *testing.T) {
	// Get a new temp directory base
	tmpDir := t.TempDir()
	newDir := filepath.Join(tmpDir, "new_folder")

	// It should create the directory
	err := CreateFolderIfMissing(newDir)
	if err != nil {
		t.Errorf("CreateFolderIfMissing returned an error: %v", err)
	}

	// Check if the directory now exists
	if !DirExists(newDir) {
		t.Errorf("Directory was not created: %s", newDir)
	}

	// Call it again on existing directory to check if it doesn't fail
	err = CreateFolderIfMissing(newDir)
	if err != nil {
		t.Errorf("CreateFolderIfMissing returned an error on existing dir: %v", err)
	}
}
