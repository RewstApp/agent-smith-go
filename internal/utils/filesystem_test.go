package utils

import (
	"os"
	"path/filepath"
	"testing"
)

// defaultFileSystem tests

func TestDefaultFileSystem_MkdirAll(t *testing.T) {
	fs := NewFileSystem()
	newDir := filepath.Join(t.TempDir(), "sub", "dir")

	err := fs.MkdirAll(newDir)

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	info, err := os.Stat(newDir)
	if err != nil || !info.IsDir() {
		t.Errorf("expected directory %s to exist", newDir)
	}

	err = fs.MkdirAll(newDir)

	if err != nil {
		t.Fatalf("expected no error on second call, got %v", err)
	}
}

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
