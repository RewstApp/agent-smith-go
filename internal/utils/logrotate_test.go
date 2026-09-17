package utils

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestRotator(t *testing.T, maxBytes int64, maxFiles int) (*RotatingFile, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.log")
	r, err := NewRotatingFile(path, maxBytes, maxFiles, DefaultFileMod)
	if err != nil {
		t.Fatalf("NewRotatingFile: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r, path
}

func mustWrite(t *testing.T, r *RotatingFile, s string) {
	t.Helper()
	n, err := r.Write([]byte(s))
	if err != nil {
		t.Fatalf("Write(%q): %v", s, err)
	}
	if n != len(s) {
		t.Fatalf("Write(%q) = %d bytes, want %d", s, n, len(s))
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	return string(b)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func TestNewRotatingFile_RejectsNonPositiveBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	if _, err := NewRotatingFile(path, 0, 1, DefaultFileMod); err == nil {
		t.Error("maxBytes=0 accepted, want error")
	}
	if _, err := NewRotatingFile(path, 10, 0, DefaultFileMod); err == nil {
		t.Error("maxFiles=0 accepted, want error")
	}
}

func TestRotatingFile_BelowCeilingDoesNotRotate(t *testing.T) {
	r, path := newTestRotator(t, 100, 3)
	mustWrite(t, r, "one\n")
	mustWrite(t, r, "two\n")

	if got := readFile(t, path); got != "one\ntwo\n" {
		t.Errorf("active = %q, want %q", got, "one\ntwo\n")
	}
	if exists(path + ".1") {
		t.Error("rotated file created below the ceiling")
	}
}

func TestRotatingFile_RotatesWhenWriteWouldCrossCeiling(t *testing.T) {
	r, path := newTestRotator(t, 10, 3)
	mustWrite(t, r, "12345678\n") // 9 bytes, fits
	mustWrite(t, r, "next\n")     // 9+5 > 10: rotate first, then write

	if got := readFile(t, path+".1"); got != "12345678\n" {
		t.Errorf("rotated .1 = %q, want the pre-rotation content", got)
	}
	if got := readFile(t, path); got != "next\n" {
		t.Errorf("active = %q, want only the post-rotation line", got)
	}
}

func TestRotatingFile_RetentionKeepsExactlyMaxFiles(t *testing.T) {
	const maxFiles = 3
	r, path := newTestRotator(t, 4, maxFiles)

	// Each line fills the file; the next line forces a rotation. Do enough to
	// overflow the retention window twice over.
	for i := 0; i < maxFiles+3; i++ {
		mustWrite(t, r, fmt.Sprintf("L%d\n", i)) // 3 bytes
	}

	for i := 1; i <= maxFiles; i++ {
		if !exists(fmt.Sprintf("%s.%d", path, i)) {
			t.Errorf("expected rotated copy .%d to exist", i)
		}
	}
	if exists(fmt.Sprintf("%s.%d", path, maxFiles+1)) {
		t.Errorf("rotated copy .%d exists beyond the retention window", maxFiles+1)
	}

	// Newest rotated copy holds the most recent pre-rotation line; the active
	// file holds the latest line.
	if got := readFile(t, path); got != fmt.Sprintf("L%d\n", maxFiles+2) {
		t.Errorf("active = %q", got)
	}
	if got := readFile(t, path+".1"); got != fmt.Sprintf("L%d\n", maxFiles+1) {
		t.Errorf(".1 = %q", got)
	}
}

func TestRotatingFile_SingleOversizedWriteOnEmptyFileIsWrittenNotRotated(t *testing.T) {
	r, path := newTestRotator(t, 5, 2)
	big := strings.Repeat("x", 50) + "\n"
	mustWrite(t, r, big)

	if got := readFile(t, path); got != big {
		t.Errorf("oversized line not written intact")
	}
	if exists(path + ".1") {
		t.Error("an empty file was rotated")
	}
}

func TestRotatingFile_PreExistingOversizedFileRotatesOnFirstWrite(t *testing.T) {
	// The upgrade path: an agent that never rotated left a large log behind.
	path := filepath.Join(t.TempDir(), "agent.log")
	legacy := strings.Repeat("old\n", 100)
	if err := os.WriteFile(path, []byte(legacy), DefaultFileMod); err != nil {
		t.Fatal(err)
	}

	r, err := NewRotatingFile(path, 64, 2, DefaultFileMod)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	mustWrite(t, r, "new\n")

	if got := readFile(t, path+".1"); got != legacy {
		t.Error("legacy content was not moved to .1 on the first write")
	}
	if got := readFile(t, path); got != "new\n" {
		t.Errorf("active = %q, want only the new line", got)
	}
}

func TestRotatingFile_RotatedCopyKeepsActiveFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits are not meaningful on Windows")
	}
	r, path := newTestRotator(t, 4, 2)
	mustWrite(t, r, "abc\n")
	mustWrite(t, r, "def\n") // rotates

	active, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := os.Stat(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if active.Mode().Perm() != rotated.Mode().Perm() {
		t.Errorf("rotated perm %v != active perm %v", rotated.Mode().Perm(), active.Mode().Perm())
	}
	if active.Mode().Perm() != DefaultFileMod {
		t.Errorf("active perm %v, want %v", active.Mode().Perm(), DefaultFileMod)
	}
}

func TestRotatingFile_RotationFailureDegradesToAppendAndReportsOncePerTransition(t *testing.T) {
	r, path := newTestRotator(t, 10, 2)

	now := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }

	renameCalls := 0
	failing := true
	r.rename = func(oldpath, newpath string) error {
		renameCalls++
		if failing {
			return errors.New("sharing violation")
		}
		return os.Rename(oldpath, newpath)
	}

	mustWrite(t, r, "12345678\n") // fills
	mustWrite(t, r, "A\n")        // rotation attempted and fails; line still written
	mustWrite(t, r, "B\n")        // still over ceiling, but suppressed: no retry, no note

	if exists(path + ".1") {
		t.Fatal("a rotated copy exists although rename was made to fail")
	}
	content := readFile(t, path)
	for _, want := range []string{"12345678\n", "A\n", "B\n"} {
		if !strings.Contains(content, want) {
			t.Errorf("line %q lost during failed rotation; file = %q", want, content)
		}
	}
	if n := strings.Count(content, "log rotation failed"); n != 1 {
		t.Errorf("failure reported %d times, want exactly once; file = %q", n, content)
	}
	if !strings.Contains(content, "[WARN]  agent_smith:") {
		t.Errorf("diagnostic is not in hclog format; file = %q", content)
	}
	if renameCalls != 1 {
		t.Errorf("rename attempted %d times within the retry window, want 1", renameCalls)
	}

	// Past the retry window the rotation is attempted again and now succeeds;
	// the recovery is reported once, into the new active file.
	failing = false
	now = now.Add(rotateRetryWindow + time.Second)
	mustWrite(t, r, "C\n")

	if !exists(path + ".1") {
		t.Fatal("rotation did not happen after the retry window elapsed")
	}
	active := readFile(t, path)
	if strings.Count(active, "log rotation recovered after 1 failure(s)") != 1 {
		t.Errorf("recovery not reported exactly once; active = %q", active)
	}
	if !strings.Contains(active, "C\n") {
		t.Errorf("post-recovery line missing; active = %q", active)
	}
	rotated := readFile(t, path+".1")
	if !strings.Contains(rotated, "A\n") || !strings.Contains(rotated, "B\n") {
		t.Errorf(
			"lines written while degraded were not carried into the rotated copy; .1 = %q",
			rotated,
		)
	}
}

func TestRotatingFile_WriteAfterCloseReopens(t *testing.T) {
	r, path := newTestRotator(t, 100, 2)
	mustWrite(t, r, "before\n")
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, r, "after\n")
	if got := readFile(t, path); got != "before\nafter\n" {
		t.Errorf("file = %q", got)
	}
}

func TestRotatingFile_OpenAppendHandleWritesCountTowardCeiling(t *testing.T) {
	r, path := newTestRotator(t, 10, 2)

	h, err := r.OpenAppendHandle()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.WriteString("external\n"); err != nil { // 9 bytes via the side handle
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}

	mustWrite(t, r, "own\n") // 9+4 > 10: the side handle's bytes must have counted

	if got := readFile(t, path+".1"); got != "external\n" {
		t.Errorf(
			"bytes written through the side handle were not counted toward the ceiling; .1 = %q",
			got,
		)
	}
	if got := readFile(t, path); got != "own\n" {
		t.Errorf("active = %q", got)
	}
}

func TestRotatingFile_PrunesCopiesBeyondMaxFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	// A previous configuration kept more copies than the new one allows.
	for _, n := range []int{1, 2, 7, 8} {
		name := fmt.Sprintf("%s.%d", path, n)
		if err := os.WriteFile(name, []byte("old\n"), DefaultFileMod); err != nil {
			t.Fatal(err)
		}
	}
	r, err := NewRotatingFile(path, 4, 3, DefaultFileMod)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()

	mustWrite(t, r, "abc\n")
	mustWrite(t, r, "def\n") // rotates -> prune runs

	for _, n := range []int{7, 8} {
		if exists(fmt.Sprintf("%s.%d", path, n)) {
			t.Errorf("copy .%d above max files survived a rotation", n)
		}
	}
	for _, n := range []int{1, 2, 3} {
		if !exists(fmt.Sprintf("%s.%d", path, n)) {
			t.Errorf("copy .%d missing", n)
		}
	}
}

func TestRotatingFile_ConcurrentWritesLoseNothing(t *testing.T) {
	const writers, perWriter = 8, 200
	line := "0123456789\n" // 11 bytes
	r, path := newTestRotator(t, 4096, 50)

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				if _, err := r.Write([]byte(line)); err != nil {
					t.Errorf("concurrent Write: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	total := 0
	matches, _ := filepath.Glob(path + "*")
	for _, m := range matches {
		content := readFile(t, m)
		if strings.Contains(content, "log rotation") {
			t.Errorf("unexpected rotation diagnostic in %s", m)
		}
		total += strings.Count(content, line)
	}
	if total != writers*perWriter {
		t.Errorf(
			"recovered %d lines across %d files, want %d",
			total,
			len(matches),
			writers*perWriter,
		)
	}
}
