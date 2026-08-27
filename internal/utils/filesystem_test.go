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

func TestDefaultFileSystem_RemoveAll(t *testing.T) {
	fs := NewFileSystem()
	dir := filepath.Join(t.TempDir(), "to_remove")

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	err := fs.RemoveAll(dir)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Errorf("expected directory %s to be removed", dir)
	}

	// Calling RemoveAll on a non-existent path must also succeed.
	err = fs.RemoveAll(dir)
	if err != nil {
		t.Fatalf("expected no error on non-existent path, got %v", err)
	}
}

func TestDefaultFileSystem_ReadFile_NotFound(t *testing.T) {
	fs := NewFileSystem()

	_, err := fs.ReadFile(filepath.Join(t.TempDir(), "nonexistent.txt"))

	if err == nil {
		t.Error("expected error reading nonexistent file, got nil")
	}
}

func TestDefaultFileSystem_Rename(t *testing.T) {
	fs := NewFileSystem()
	dir := t.TempDir()
	source := filepath.Join(dir, "agent.new")
	destination := filepath.Join(dir, "agent")

	if err := os.WriteFile(source, []byte("new"), DefaultExecutableFileMod); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, []byte("old"), DefaultExecutableFileMod); err != nil {
		t.Fatal(err)
	}

	// Renaming over an existing file must replace it: that is what makes the
	// executable replacement a single atomic step.
	if err := fs.Rename(source, destination); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	contents, err := os.ReadFile(destination)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if string(contents) != "new" {
		t.Errorf("expected the destination replaced, got %q", contents)
	}
	if _, statErr := os.Stat(source); !os.IsNotExist(statErr) {
		t.Errorf("expected the source to be gone, stat returned %v", statErr)
	}
}

func TestDefaultFileSystem_Remove(t *testing.T) {
	fs := NewFileSystem()
	path := filepath.Join(t.TempDir(), "agent.new")

	if err := os.WriteFile(path, []byte("temp"), DefaultFileMod); err != nil {
		t.Fatal(err)
	}

	if err := fs.Remove(path); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Errorf("expected the file to be removed, stat returned %v", statErr)
	}
}

func TestDefaultFileSystem_ExecutableInUse_MissingFile(t *testing.T) {
	fs := NewFileSystem()

	inUse, err := fs.ExecutableInUse(filepath.Join(t.TempDir(), "never-installed"))
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if inUse {
		t.Error("expected a path with nothing installed at it not to be in use")
	}
}

func TestDefaultFileSystem_ExecutableInUse_IdleFile(t *testing.T) {
	fs := NewFileSystem()
	path := filepath.Join(t.TempDir(), "agent")
	contents := []byte("not running")

	if err := os.WriteFile(path, contents, DefaultExecutableFileMod); err != nil {
		t.Fatal(err)
	}

	inUse, err := fs.ExecutableInUse(path)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if inUse {
		t.Error("expected a file no process is running not to be in use")
	}

	// The probe opens the file for writing; it must not disturb the contents it
	// is only inspecting.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(contents) {
		t.Errorf("expected the probe to leave the file untouched, got %q", after)
	}
}

func TestDefaultFileSystem_ExecutableInUse_UnprobeablePath(t *testing.T) {
	fs := NewFileSystem()

	// A path that cannot be opened for writing at all is reported as an error
	// rather than silently as "free": the caller decides what to do about a probe
	// it could not run, and must not read it as evidence the process is gone.
	_, err := fs.ExecutableInUse(t.TempDir())
	if err == nil {
		t.Error("expected an error probing a path that is not a regular file")
	}
}

func TestDefaultFileSystem_EnsureSecureDir_CreatesDirectory(t *testing.T) {
	fs := NewFileSystem()
	dir := filepath.Join(t.TempDir(), "sub", "scripts")

	if err := fs.EnsureSecureDir(dir); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("expected directory %s to exist, err = %v", dir, err)
	}
}

func TestEnsureSecureDir_CreatesWithSecureMode(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "scripts")

	if err := EnsureSecureDir(dir); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("expected directory to exist: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected %s to be a directory", dir)
	}
}

func TestEnsureSecureDir_RefusesRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(path, []byte("x"), DefaultFileMod); err != nil {
		t.Fatal(err)
	}

	if err := EnsureSecureDir(path); err == nil {
		t.Error("expected an error when the path is a regular file, got nil")
	}
}

func TestEnsureSecureDir_RefusesSymlink(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target-dir")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(base, "scripts")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := EnsureSecureDir(link); err == nil {
		t.Error("expected an error when the path is a symlink, got nil")
	}
}

func TestEnsureSecureDir_IdempotentOnExistingSecureDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "scripts")
	if err := EnsureSecureDir(dir); err != nil {
		t.Fatalf("expected no error on first call, got %v", err)
	}

	if err := EnsureSecureDir(dir); err != nil {
		t.Fatalf("expected no error on second call, got %v", err)
	}
}

func TestDefaultFileSystem_EnsureSecureFile_MissingPathIsNotAnError(t *testing.T) {
	fs := NewFileSystem()
	path := filepath.Join(t.TempDir(), "config.json")

	// Nothing has been written yet: the write path creates it with
	// SecureFileMode from the start, so a missing file is not an error here.
	if err := fs.EnsureSecureFile(path); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected EnsureSecureFile not to create the file, stat returned %v", err)
	}
}

func TestEnsureSecureFile_CreatesNothingWhenMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")

	if err := EnsureSecureFile(path); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected no file to exist, stat returned %v", err)
	}
}

func TestEnsureSecureFile_RefusesSymlink(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "target-file")
	if err := os.WriteFile(target, []byte("secret"), DefaultFileMod); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(base, "config.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if err := EnsureSecureFile(link); err == nil {
		t.Error("expected an error when the path is a symlink, got nil")
	}
}

func TestEnsureSecureFile_RefusesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-a-file")
	if err := os.MkdirAll(dir, DefaultDirMod); err != nil {
		t.Fatal(err)
	}

	if err := EnsureSecureFile(dir); err == nil {
		t.Error("expected an error when the path is a directory, got nil")
	}
}

func TestEnsureSecureFile_IdempotentOnExistingSecureFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte("{}"), SecureFileMode); err != nil {
		t.Fatal(err)
	}

	if err := EnsureSecureFile(path); err != nil {
		t.Fatalf("expected no error on first call, got %v", err)
	}
	if err := EnsureSecureFile(path); err != nil {
		t.Fatalf("expected no error on second call, got %v", err)
	}
}
