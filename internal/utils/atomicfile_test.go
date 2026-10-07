package utils

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// recordingFS wraps the real filesystem and records every call the atomic
// write makes, in order, so the tests can assert on the sequence rather than
// only on the end state.
type recordingFS struct {
	FileSystem
	calls   []string
	syncErr func(name string) error
}

func (r *recordingFS) WriteFile(name string, data []byte, perm os.FileMode) error {
	r.calls = append(r.calls, "write "+filepath.Base(name))
	return r.FileSystem.WriteFile(name, data, perm)
}

func (r *recordingFS) Sync(name string) error {
	r.calls = append(r.calls, "sync "+filepath.Base(name))
	if r.syncErr != nil {
		if err := r.syncErr(name); err != nil {
			return err
		}
	}
	return r.FileSystem.Sync(name)
}

func (r *recordingFS) Rename(oldPath, newPath string) error {
	r.calls = append(r.calls, "rename "+filepath.Base(oldPath)+" "+filepath.Base(newPath))
	return r.FileSystem.Rename(oldPath, newPath)
}

func (r *recordingFS) Remove(name string) error {
	r.calls = append(r.calls, "remove "+filepath.Base(name))
	return r.FileSystem.Remove(name)
}

func seed(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), DefaultFileMod); err != nil {
		t.Fatal(err)
	}
}

func readBack(t *testing.T, path string) string {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(got)
}

func noTempLeft(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path + AtomicTempSuffix); !os.IsNotExist(err) {
		t.Errorf("expected the temporary file to be gone, stat returned %v", err)
	}
}

// The durability boundary: the data is synced before it is renamed into place,
// and the directory entry is synced after, so a power loss at any instant
// leaves either the previous file or the complete, on-disk new one.
func TestWriteFileAtomic_SyncsTheFileBeforeTheRenameAndTheDirectoryAfter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "record.json")
	fsys := &recordingFS{FileSystem: NewFileSystem()}

	if err := WriteFileAtomic(fsys, path, []byte(`{"k":1}`), DefaultFileMod); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"write record.json" + AtomicTempSuffix,
		"sync record.json" + AtomicTempSuffix,
		"rename record.json" + AtomicTempSuffix + " record.json",
		"sync " + filepath.Base(dir),
	}
	if strings.Join(fsys.calls, "\n") != strings.Join(want, "\n") {
		t.Errorf(
			"call sequence:\n%s\nwant:\n%s",
			strings.Join(fsys.calls, "\n"),
			strings.Join(want, "\n"),
		)
	}
	if got := readBack(t, path); got != `{"k":1}` {
		t.Errorf("content %q", got)
	}
	noTempLeft(t, path)
}

func TestWriteFileAtomic_ReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent")
	seed(t, path, "old binary")

	err := WriteFileAtomic(NewFileSystem(), path, []byte("new binary"), DefaultExecutableFileMod)
	if err != nil {
		t.Fatal(err)
	}
	if got := readBack(t, path); got != "new binary" {
		t.Errorf("expected the new contents, got %q", got)
	}
	noTempLeft(t, path)
}

// A sync that fails means the bytes may not be on disk: the write must not be
// committed, the previous file must be untouched, and the temp file must not
// be left for a retry to inherit.
func TestWriteFileAtomic_FailedFileSyncLeavesOriginalIntactAndNoTemp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record.json")
	seed(t, path, "original")
	fsys := &recordingFS{FileSystem: NewFileSystem(), syncErr: func(name string) error {
		if strings.HasSuffix(name, AtomicTempSuffix) {
			return errors.New("input/output error")
		}
		return nil
	}}

	err := WriteFileAtomic(fsys, path, []byte("new"), DefaultFileMod)
	if err == nil || !strings.Contains(err.Error(), "input/output error") {
		t.Fatalf("expected the sync failure, got %v", err)
	}
	if got := readBack(t, path); got != "original" {
		t.Errorf("original replaced: %q", got)
	}
	noTempLeft(t, path)
	for _, c := range fsys.calls {
		if strings.HasPrefix(c, "rename ") {
			t.Errorf("renamed despite the failed sync: %v", fsys.calls)
		}
	}
}

// failingRenameFS commits nothing: it models the destination being unwritable
// at the moment of the rename, which is exactly the sharing violation Windows
// raises when the old process is still holding the image.
type failingRenameFS struct{ FileSystem }

func (f *failingRenameFS) Rename(string, string) error { return errors.New("sharing violation") }

// A failed commit must leave the installed binary byte-identical rather than
// truncated: the endpoint keeps running the old agent instead of nothing at all.
func TestWriteFileAtomic_FailedCommitLeavesOriginalIntact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent")
	seed(t, path, "old binary contents")

	err := WriteFileAtomic(
		&failingRenameFS{NewFileSystem()},
		path,
		[]byte("new"),
		DefaultExecutableFileMod,
	)
	if err == nil {
		t.Fatal("expected the write to fail")
	}
	if got := readBack(t, path); got != "old binary contents" {
		t.Errorf("expected the original contents preserved, got %q", got)
	}
	noTempLeft(t, path)
}

type failingWriteFS struct {
	FileSystem
	t *testing.T
}

func (f *failingWriteFS) WriteFile(name string, _ []byte, _ os.FileMode) error {
	if !strings.HasSuffix(name, AtomicTempSuffix) {
		f.t.Errorf("expected the write to target a temporary file, got %q", name)
	}
	return errors.New("no space left on device")
}

func TestWriteFileAtomic_WriteFailureLeavesOriginalIntact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent")
	seed(t, path, "old binary contents")

	fsys := &failingWriteFS{NewFileSystem(), t}
	err := WriteFileAtomic(fsys, path, []byte("new"), DefaultExecutableFileMod)
	if err == nil {
		t.Fatal("expected the write to fail")
	}
	if got := readBack(t, path); got != "old binary contents" {
		t.Errorf("expected the original contents preserved, got %q", got)
	}
}

// The directory sync failing after a successful rename is reported, not hidden:
// the file is in place but the caller's "durably accepted" would otherwise be
// a claim it cannot back.
func TestWriteFileAtomic_FailedDirectorySyncIsReported(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "record.json")
	fsys := &recordingFS{FileSystem: NewFileSystem(), syncErr: func(name string) error {
		if name == dir {
			return errors.New("input/output error")
		}
		return nil
	}}

	err := WriteFileAtomic(fsys, path, []byte("new"), DefaultFileMod)
	if err == nil || !strings.Contains(err.Error(), "sync directory") {
		t.Fatalf("expected the directory sync failure, got %v", err)
	}
	if got := readBack(t, path); got != "new" {
		t.Errorf("the committed file should be in place, got %q", got)
	}
}

// Sync on the real filesystem works for a file and for a directory (a no-op on
// Windows for the directory), and reports a missing path.
func TestDefaultFileSystem_Sync(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	seed(t, path, "x")
	fsys := NewFileSystem()
	if err := fsys.Sync(path); err != nil {
		t.Errorf("sync file: %v", err)
	}
	if err := fsys.Sync(dir); err != nil {
		t.Errorf("sync dir: %v", err)
	}
	if err := fsys.Sync(filepath.Join(dir, "missing")); err == nil {
		t.Error("expected an error for a missing path")
	}
}

// The cost of durability, recorded rather than asserted: the acceptance
// criterion for sc-119835 is a median under 20 ms per write on the CI runners,
// and the Test workflow runs this with -v so the number lands in the job log
// for every platform. It is not a hard assertion because a starved runner
// would make it flaky without saying anything about the agent.
func TestWriteFileAtomic_MedianCost(t *testing.T) {
	dir := t.TempDir()
	fsys := NewFileSystem()
	const n = 21
	durations := make([]time.Duration, 0, n)
	payload := []byte(strings.Repeat("x", 2048))
	for i := 0; i < n; i++ {
		path := filepath.Join(dir, "entry-"+time.Now().Format("150405.000000000")+".json")
		start := time.Now()
		if err := WriteFileAtomic(fsys, path, payload, DefaultFileMod); err != nil {
			t.Fatal(err)
		}
		durations = append(durations, time.Since(start))
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	t.Logf("WriteFileAtomic median cost over %d writes: %s (min %s, max %s)",
		n, durations[n/2], durations[0], durations[n-1])
}
