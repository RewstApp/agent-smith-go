package utils

import (
	"os"
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

func TestDirExists_File(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "file.txt")

	if err := os.WriteFile(filePath, []byte("data"), DefaultFileMod); err != nil {
		t.Fatal(err)
	}

	if DirExists(filePath) {
		t.Errorf("expected false for a file path, got true")
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

// defaultFileSystem tests

func TestNewFileSystem_ReturnsNonNil(t *testing.T) {
	fs := NewFileSystem()
	if fs == nil {
		t.Fatal("expected non-nil FileSystem")
	}
}

func TestDefaultFileSystem_Executable(t *testing.T) {
	fs := NewFileSystem()

	path, err := fs.Executable()

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if path == "" {
		t.Error("expected non-empty executable path")
	}
}

func TestDefaultFileSystem_WriteFile(t *testing.T) {
	fs := NewFileSystem()
	filePath := filepath.Join(t.TempDir(), "test.txt")
	data := []byte("hello")

	err := fs.WriteFile(filePath, data, DefaultFileMod)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	got, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("failed to read back file: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("expected %q, got %q", data, got)
	}
}

func TestDefaultFileSystem_ReadFile(t *testing.T) {
	fs := NewFileSystem()
	filePath := filepath.Join(t.TempDir(), "test.txt")
	data := []byte("hello")

	if err := os.WriteFile(filePath, data, DefaultFileMod); err != nil {
		t.Fatal(err)
	}

	got, err := fs.ReadFile(filePath)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("expected %q, got %q", data, got)
	}
}

func TestDefaultFileSystem_ReadFile_NotFound(t *testing.T) {
	fs := NewFileSystem()

	_, err := fs.ReadFile(filepath.Join(t.TempDir(), "nonexistent.txt"))

	if err == nil {
		t.Error("expected error reading nonexistent file, got nil")
	}
}
