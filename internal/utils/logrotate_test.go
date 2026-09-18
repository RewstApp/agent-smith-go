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

const testSource = "agent_smith"

func newTestRotatorAt(t *testing.T, path string, maxBytes int64, maxFiles int) *RotatingFile {
	t.Helper()
	r, err := NewRotatingFile(path, maxBytes, maxFiles, DefaultFileMod, testSource)
	if err != nil {
		t.Fatalf("NewRotatingFile: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func newTestRotator(t *testing.T, maxBytes int64, maxFiles int) (*RotatingFile, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.log")
	return newTestRotatorAt(t, path, maxBytes, maxFiles), path
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

// freezeClock pins the writer's clock so size refreshes and retry windows are
// deterministic; the returned func advances it.
func freezeClock(r *RotatingFile) func(time.Duration) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	r.now = func() time.Time { return now }
	// open() already stamped lastStat from the real clock; bring it onto the
	// frozen one so elapsed-time checks measure only the advances below.
	r.lastStat = now
	return func(d time.Duration) { now = now.Add(d) }
}

func TestNewRotatingFile_RejectsNonPositiveBounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.log")
	if _, err := NewRotatingFile(path, 0, 1, DefaultFileMod, testSource); err == nil {
		t.Error("maxBytes=0 accepted, want error")
	}
	if _, err := NewRotatingFile(path, 10, 0, DefaultFileMod, testSource); err == nil {
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

	for i := 0; i < maxFiles+3; i++ {
		mustWrite(t, r, fmt.Sprintf("L%d\n", i)) // 3 bytes each; every line after the first rotates
	}

	for i := 1; i <= maxFiles; i++ {
		if !exists(fmt.Sprintf("%s.%d", path, i)) {
			t.Errorf("expected rotated copy .%d to exist", i)
		}
	}
	if exists(fmt.Sprintf("%s.%d", path, maxFiles+1)) {
		t.Errorf("rotated copy .%d exists beyond the retention window", maxFiles+1)
	}
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
	r := newTestRotatorAt(t, path, 64, 2)

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

func TestRotatingFile_RenameFailureDegradesToAppendAndReportsOncePerTransition(t *testing.T) {
	r, path := newTestRotator(t, 10, 2)
	advance := freezeClock(r)

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
	if !strings.Contains(content, "[WARN]  "+testSource+":") {
		t.Errorf("diagnostic is not in hclog format with the caller's source; file = %q", content)
	}
	if renameCalls != 1 {
		t.Errorf("rename attempted %d times within the retry window, want 1", renameCalls)
	}

	// Past the retry window the rotation is attempted again and now succeeds;
	// the recovery is reported once, into the new active file.
	failing = false
	advance(rotateRetryWindow + time.Second)
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

func TestRotatingFile_ReopenFailureAfterRenameIsNotARotationFailure(t *testing.T) {
	// A ceiling large enough that the recovery note itself does not cross it.
	r, path := newTestRotator(t, 1000, 2)
	freezeClock(r)
	fill := strings.Repeat("x", 998) + "\n" // 999 bytes: the next 2-byte line crosses 1000

	failOpens := 0
	r.openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		if failOpens > 0 {
			failOpens--
			return nil, errors.New("too many open files")
		}
		return os.OpenFile(name, flag, perm)
	}

	mustWrite(t, r, fill)

	// Rotation: rename succeeds, the reopen of the new active file fails.
	failOpens = 1
	if _, err := r.Write([]byte("A\n")); err == nil {
		t.Fatal("Write succeeded although the log could not be reopened")
	}
	if got := readFile(t, path+".1"); got != fill {
		t.Errorf("rotation did not happen; .1 has %d bytes", len(got))
	}
	if exists(path) {
		t.Error("an active file exists although reopen was made to fail")
	}
	if r.degraded {
		t.Error("a reopen failure was recorded as a degraded (failed) rotation")
	}

	// A second write while still closed is also lost and counted.
	failOpens = 1
	if _, err := r.Write([]byte("B\n")); err == nil {
		t.Fatal("second Write succeeded while the log was closed")
	}

	// The next write reopens, owes the file one note naming what was lost,
	// then lands.
	mustWrite(t, r, "C\n")
	active := readFile(t, path)
	if n := strings.Count(active, "log rotated but the new file could not be opened"); n != 1 {
		t.Errorf("reopen failure reported %d times, want exactly once; active = %q", n, active)
	}
	if !strings.Contains(active, "2 write(s) were lost") {
		t.Errorf("lost-write count missing or wrong; active = %q", active)
	}
	if strings.Contains(active, "log rotation failed") || strings.Contains(active, "recovered") {
		t.Errorf("reopen failure misreported as a rotation failure/recovery; active = %q", active)
	}
	if !strings.HasSuffix(active, "C\n") {
		t.Errorf("post-reopen line missing; active = %q", active)
	}

	// Rotation is not suppressed by a reopen failure: the next crossing rotates
	// immediately and, since rotation never failed, says nothing about it.
	mustWrite(t, r, fill)
	rotated := readFile(t, path+".1")
	if !strings.Contains(rotated, "C\n") || !strings.Contains(rotated, "write(s) were lost") {
		t.Errorf(
			"the crossing after recovery did not rotate immediately; .1 = %q",
			rotated[:min(len(rotated), 120)],
		)
	}
	if got := readFile(t, path); got != fill {
		t.Errorf(
			"active after the post-recovery rotation = %d bytes, want the fill line only",
			len(got),
		)
	}
	for _, f := range []string{path, path + ".1"} {
		c := readFile(t, f)
		if strings.Contains(c, "recovered") || strings.Contains(c, "rotation failed") {
			t.Errorf(
				"%s carries a rotation failure/recovery note although rotation never failed",
				f,
			)
		}
	}
}

func TestRotatingFile_RotatesOnlyAtLineBoundaries(t *testing.T) {
	r, path := newTestRotator(t, 10, 2)
	mustWrite(t, r, "12345678\n") // 9 bytes; file ends at a line boundary

	// A line delivered in two chunks, as go-plugin copies plugin stderr. The
	// first chunk crosses the ceiling at a boundary, so rotation happens before
	// it; the second chunk completes the line in the same (new) file.
	mustWrite(t, r, "part")
	mustWrite(t, r, "ial\n")

	if got := readFile(t, path+".1"); got != "12345678\n" {
		t.Errorf(".1 = %q", got)
	}
	if got := readFile(t, path); got != "partial\n" {
		t.Errorf("line split across files or rotated mid-line; active = %q", got)
	}

	// A crossing that arrives mid-line is deferred until the line completes.
	mustWrite(t, r, "xxxxxx")   // 8+6 > 10 at a boundary: rotates -> active "xxxxxx"
	mustWrite(t, r, "yyyyyyyy") // 14 > 10 but mid-line and under 2x: deferred
	mustWrite(t, r, "z\n")      // still deferred; line now complete
	if got := readFile(t, path); got != "xxxxxxyyyyyyyyz\n" {
		t.Errorf("mid-line rotation split a line; active = %q", got)
	}
	mustWrite(t, r, "next\n") // at a boundary again: rotates
	if got := readFile(t, path); got != "next\n" {
		t.Errorf("rotation did not resume at the next boundary; active = %q", got)
	}
}

func TestRotatingFile_ForcesRotationAtTwiceTheCeilingForUnterminatedWriter(t *testing.T) {
	r, path := newTestRotator(t, 10, 2)
	for i := 0; i < 6; i++ {
		mustWrite(t, r, "aaaa") // never ends a line
	}
	// Sizes: 4, 8, 12 (deferred), 16 (deferred), 20 (deferred), then the sixth
	// write finds size >= 2*max and rotates regardless of the boundary.
	if got := readFile(t, path+".1"); got != strings.Repeat("a", 20) {
		t.Errorf("forced rotation did not happen at twice the ceiling; .1 = %q", got)
	}
	if got := readFile(t, path); got != "aaaa" {
		t.Errorf("active = %q", got)
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

func TestRotatingFile_SideHandleBytesCountViaDirtyFlagNotPerWriteStat(t *testing.T) {
	r, path := newTestRotator(t, 10, 2)
	freezeClock(r) // no time-based refresh: only the dirty flag can trigger one

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

	mustWrite(t, r, "own\n") // 9+4 > 10 once the refresh has counted the side handle's bytes

	if got := readFile(t, path+".1"); got != "external\n" {
		t.Errorf("side-handle bytes were not counted toward the ceiling; .1 = %q", got)
	}
	if got := readFile(t, path); got != "own\n" {
		t.Errorf("active = %q", got)
	}
}

func TestRotatingFile_SizeRefreshIsRateLimited(t *testing.T) {
	r, path := newTestRotator(t, 100, 2)
	advance := freezeClock(r)
	mustWrite(t, r, "a\n")

	// Bytes appended behind the writer's back are invisible until a refresh.
	if err := os.WriteFile(path, []byte(strings.Repeat("b", 200)), DefaultFileMod); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, r, "c\n")
	if exists(path + ".1") {
		t.Fatal("size was re-read within the refresh interval")
	}

	// After the interval the next write refreshes, sees the file over the
	// ceiling at a boundary, and rotates.
	advance(sizeRefreshInterval)
	mustWrite(t, r, "d\n")
	if !exists(path + ".1") {
		t.Error("size was not refreshed after the interval elapsed")
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
	r := newTestRotatorAt(t, path, 4, 3)

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

func TestRotatingFile_PathWithGlobMetacharactersStillPrunesAndShifts(t *testing.T) {
	// The data directory embeds the org id; nothing here may depend on the path
	// being free of characters that mean something to a glob. '[' is the one
	// that makes filepath.Glob fail outright (ErrBadPattern) rather than merely
	// mismatch, and unlike '*' and '?' it is a legal filename character on
	// Windows too, so the test runs on every platform.
	dir := filepath.Join(t.TempDir(), "org[1]")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "agent.log")
	if err := os.WriteFile(path+".9", []byte("stale\n"), DefaultFileMod); err != nil {
		t.Fatal(err)
	}
	r := newTestRotatorAt(t, path, 4, 2)

	mustWrite(t, r, "abc\n")
	mustWrite(t, r, "def\n") // rotates

	if exists(path + ".9") {
		t.Error("copy above max files survived: enumeration was defeated by the path")
	}
	if got := readFile(t, path+".1"); got != "abc\n" {
		t.Errorf(".1 = %q", got)
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
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		content := readFile(t, filepath.Join(filepath.Dir(path), e.Name()))
		if strings.Contains(content, "log rotat") {
			t.Errorf("unexpected rotation diagnostic in %s", e.Name())
		}
		total += strings.Count(content, line)
	}
	if total != writers*perWriter {
		t.Errorf(
			"recovered %d lines across %d files, want %d",
			total,
			len(entries),
			writers*perWriter,
		)
	}
}
