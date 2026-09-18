package utils

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// rotateRetryWindow is how long RotatingFile keeps appending to the current
	// file after a rename fails before it tries to rotate again. Rotation is
	// attempted on the write that would cross the size ceiling, so without a
	// window every subsequent line would re-attempt the rename - and on Windows
	// the usual failure (another process holding the file open without
	// FILE_SHARE_DELETE, e.g. the detached --update helper's inherited stdout)
	// persists for as long as that process runs.
	rotateRetryWindow = time.Minute

	// sizeRefreshInterval bounds how often Write re-reads the file size from the
	// handle instead of trusting its own running total. The total is exact for
	// bytes written here; the re-read exists only to account for bytes appended
	// through a side handle (OpenAppendHandle), which is opened once per
	// auto-update and written for the seconds the helper takes to start. An
	// fstat per log line to cover that was the wrong trade on the hot path
	// every worker shares; one per second bounds the drift to a second of the
	// helper's output, which is well inside the ceiling's one-write slack.
	sizeRefreshInterval = time.Second

	// forcedRotationMultiple is the point past the ceiling at which a rotation
	// is performed even mid-line. Rotation prefers a line boundary so a writer
	// that delivers a line in several chunks (go-plugin copies a plugin's
	// stderr through in whatever pieces the pipe returns) never has one line
	// split across two files with a rotation note in the middle. A writer that
	// never terminates a line would otherwise defer rotation forever, so at
	// twice the ceiling the file is rotated regardless.
	forcedRotationMultiple = 2
)

// RotatingFile is an append-only log file writer that caps its own on-disk
// footprint. When a write would carry the active file past maxBytes the file is
// rotated first: path becomes path.1, the previous path.1 becomes path.2, and so
// on up to path.maxFiles, which is discarded. The worst-case footprint is
// (maxFiles+1) * maxBytes plus one write's overshoot (or, for a writer that
// never ends a line, up to forcedRotationMultiple * maxBytes for the active
// file).
//
// The writer owns the file handle and closes it before renaming. That ordering
// is what makes rotation work on Windows, where a file another handle has open
// without FILE_SHARE_DELETE cannot be renamed; the agent's own handle is the
// common one, and closing it first means the common case succeeds. A handle
// held by another process without that flag - the detached --update helper
// writing its inherited stdout - still blocks the rename on Windows for as long
// as it runs. Rotation failure is non-fatal: the current file is reopened and
// appending continues, one WARN line records the failure, another records the
// eventual recovery, and nothing is written in between, so a rotation that
// stays blocked cannot flood the file it is failing to rotate. This is the same
// "best effort, reported once per transition" posture the syslog forwarder
// takes.
//
// Two failures are kept distinct because they mean different things. A failed
// rename is a failed rotation: the old file is still at path, appending resumes
// there, and further attempts are suppressed for rotateRetryWindow. A rename
// that succeeded but whose reopen then failed (out of descriptors, an ACL reset
// mid-flight, a full disk refusing a new inode) is a rotation that worked and a
// log that is momentarily closed: nothing at path, so there is nothing to
// append to; every Write until the reopen succeeds returns an error and is
// counted, the next Write retries the open, and when it succeeds one WARN line
// records how many writes were lost. That case is never reported as a rotation
// failure and never produces a "recovered" line, because rotation did not fail.
//
// A line accepted by Write is never lost to rotation: rotation happens before
// the write, under the same lock, and the write then lands in whichever file is
// open afterwards.
type RotatingFile struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	maxFiles int
	mode     os.FileMode
	source   string

	f           *os.File
	size        int64
	sizeDirty   bool
	lastStat    time.Time
	atLineStart bool

	// Rename-failure (degraded rotation) state.
	degraded bool
	failures int
	retryAt  time.Time

	// Reopen-failure state: a note owed to the file once it can be opened.
	pendingNote   string
	droppedWrites int

	// Injectable for tests: forcing a rename or open failure exercises the two
	// failure paths without depending on platform-specific sharing semantics.
	rename   func(oldpath, newpath string) error
	openFile func(name string, flag int, perm os.FileMode) (*os.File, error)
	now      func() time.Time
}

// NewRotatingFile opens (creating if necessary) path for appending with the
// given mode and returns a writer that rotates it at maxBytes, keeping at most
// maxFiles rotated copies. source is the logger name stamped on the writer's
// own diagnostics; pass the same name the hclog logger writing through it uses.
// Both bounds must be positive; callers resolve defaults before getting here
// (see agent.Device.ResolvedLogMaxBytes).
func NewRotatingFile(
	path string,
	maxBytes int64,
	maxFiles int,
	mode os.FileMode,
	source string,
) (*RotatingFile, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("log rotation: max bytes must be positive, got %d", maxBytes)
	}
	if maxFiles <= 0 {
		return nil, fmt.Errorf("log rotation: max files must be positive, got %d", maxFiles)
	}

	r := &RotatingFile{
		path:     path,
		maxBytes: maxBytes,
		maxFiles: maxFiles,
		mode:     mode,
		source:   source,
		rename:   os.Rename,
		openFile: os.OpenFile,
		now:      time.Now,
	}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

// Path returns the path of the active log file.
func (r *RotatingFile) Path() string {
	return r.path
}

// OpenAppendHandle returns a fresh append-mode handle on the active log file,
// for a consumer that needs a real file descriptor rather than an io.Writer -
// the detached --update helper inherits it as its stdout/stderr and must keep
// working after this process has exited. The caller owns the handle and should
// close its copy once the child has started. Bytes written through it are
// picked up by the next Write's size refresh.
func (r *RotatingFile) OpenAppendHandle() (*os.File, error) {
	r.mu.Lock()
	r.sizeDirty = true
	r.mu.Unlock()
	return r.openFile(r.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, r.mode)
}

// Write appends p to the active file, rotating first if the write would carry
// it past the size ceiling and the file is at a line boundary. The returned
// (n, err) describe the write itself; a rotation failure never surfaces here,
// only in the file as a WARN line.
func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.f == nil {
		// A previous rotation lost the handle (reopen failed). Keep trying on
		// every write so logging resumes as soon as it can, and count what is
		// lost meanwhile so the note written on recovery can say so.
		if err := r.open(); err != nil {
			r.droppedWrites++
			return 0, err
		}
		r.flushPendingNoteLocked()
	}

	r.refreshSizeLocked()

	var note string
	if r.shouldRotateLocked(int64(len(p))) {
		note = r.rotateLocked()
		if r.f == nil {
			// Rename succeeded, reopen did not; see the type comment.
			r.droppedWrites++
			return 0, errors.New("log file is not open after rotation")
		}
	}

	if note != "" {
		// Best effort: a diagnostic about logging must never displace the log
		// line it is describing.
		if k, err := r.f.WriteString(note); err == nil {
			r.size += int64(k)
		}
	}

	n, err := r.f.Write(p)
	r.size += int64(n)
	if n > 0 {
		r.atLineStart = p[n-1] == '\n'
	}
	return n, err
}

// Close closes the active file. Further writes reopen it.
func (r *RotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}

// shouldRotateLocked decides whether the write of n bytes must be preceded by
// a rotation. size > 0 so a single write larger than the whole ceiling is
// written to an empty file rather than rotating an empty file forever.
func (r *RotatingFile) shouldRotateLocked(n int64) bool {
	if r.size <= 0 || r.size+n <= r.maxBytes {
		return false
	}
	if r.atLineStart {
		return true
	}
	return r.size >= forcedRotationMultiple*r.maxBytes
}

func (r *RotatingFile) open() error {
	f, err := r.openFile(r.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, r.mode)
	if err != nil {
		return err
	}
	fi, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	r.f = f
	r.size = fi.Size()
	r.sizeDirty = false
	r.lastStat = r.now()
	// An append-only handle cannot read the last byte back, so a pre-existing
	// file is assumed to end at a line boundary - every writer that reaches
	// this file (hclog, the syslog wrapper, this writer's own notes) terminates
	// its lines, and the cost of being wrong is one rotation that lands one
	// line early.
	r.atLineStart = true
	return nil
}

// refreshSizeLocked re-reads the size from the handle when a side handle may
// have appended to the file, or at most once per sizeRefreshInterval.
func (r *RotatingFile) refreshSizeLocked() {
	now := r.now()
	if !r.sizeDirty && now.Sub(r.lastStat) < sizeRefreshInterval {
		return
	}
	if fi, err := r.f.Stat(); err == nil {
		r.size = fi.Size()
	}
	r.sizeDirty = false
	r.lastStat = now
}

// flushPendingNoteLocked writes the note owed after a reopen failure, now that
// the file is open again.
func (r *RotatingFile) flushPendingNoteLocked() {
	if r.pendingNote == "" {
		return
	}
	note := r.note(fmt.Sprintf(
		"%s; %d write(s) were lost until the file could be reopened",
		r.pendingNote, r.droppedWrites,
	))
	if k, err := r.f.WriteString(note); err == nil {
		r.size += int64(k)
	}
	r.pendingNote = ""
	r.droppedWrites = 0
	r.atLineStart = true
}

// rotateLocked performs one rotation attempt (or skips it while suppressed
// after a rename failure) and returns the diagnostic to write into the file:
// one line when rotation starts failing, one when it recovers, nothing
// otherwise. A reopen failure is not reported here - there is no open file to
// report into - but recorded for flushPendingNoteLocked.
func (r *RotatingFile) rotateLocked() string {
	if r.degraded && r.now().Before(r.retryAt) {
		return ""
	}

	// Close before renaming so the rename can succeed on Windows.
	if r.f != nil {
		_ = r.f.Close()
		r.f = nil
	}

	renameErr := r.shiftAndRename()

	// Reopen regardless of the outcome: after a successful rename this creates
	// the new active file; after a failed one it reopens the same file so
	// appending continues.
	if openErr := r.open(); openErr != nil {
		if renameErr == nil {
			// Rotation worked; the log is momentarily closed. Owed to the file
			// once it opens again, so the gap is visible in the record.
			r.pendingNote = fmt.Sprintf(
				"log rotated but the new file could not be opened: %v", openErr,
			)
			return r.recordRotationResult(nil)
		}
		// Neither the rename nor the reopen of the old file worked: report the
		// rename failure (that is the rotation outcome) and let Write surface
		// the closed file; the next Write retries the open.
	}

	return r.recordRotationResult(renameErr)
}

// shiftAndRename discards rotated copies beyond maxFiles, shifts the rest up
// one slot from the highest down, and moves the active file into slot 1. Only
// the final rename is load-bearing: a shift that fails just means one rotated
// copy is overwritten a step early, which costs history, not the active log.
// The copies are enumerated from the directory rather than probed one slot at
// a time, so the cost tracks the files present rather than the configured
// maximum, and the enumeration has no pattern semantics a path containing glob
// metacharacters could defeat.
func (r *RotatingFile) shiftAndRename() error {
	indices := r.existingBackupIndices()

	// Descending, so a shift never overwrites a copy that has not moved yet.
	sort.Sort(sort.Reverse(sort.IntSlice(indices)))
	for _, i := range indices {
		src := r.backupPath(i)
		if i >= r.maxFiles {
			_ = os.Remove(src)
			continue
		}
		_ = r.rename(src, r.backupPath(i+1))
	}

	if err := r.rename(r.path, r.backupPath(1)); err != nil {
		return fmt.Errorf("rotate %s: %w", r.path, err)
	}
	return nil
}

// existingBackupIndices returns the numeric suffixes of the rotated copies
// currently beside the active file.
func (r *RotatingFile) existingBackupIndices() []int {
	entries, err := os.ReadDir(filepath.Dir(r.path))
	if err != nil {
		return nil
	}
	prefix := filepath.Base(r.path) + "."
	var indices []int
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), prefix) {
			continue
		}
		n, convErr := strconv.Atoi(strings.TrimPrefix(e.Name(), prefix))
		if convErr == nil && n >= 1 {
			indices = append(indices, n)
		}
	}
	return indices
}

func (r *RotatingFile) backupPath(i int) string {
	return fmt.Sprintf("%s.%d", r.path, i)
}

// recordRotationResult folds one rename outcome into the degraded-rotation
// state and returns the diagnostic for the file, mirroring the syslog
// forwarder's once-per-transition reporting.
func (r *RotatingFile) recordRotationResult(err error) string {
	if err == nil {
		if !r.degraded {
			return ""
		}
		note := r.note(fmt.Sprintf(
			"log rotation recovered after %d failure(s)", r.failures,
		))
		r.degraded = false
		r.failures = 0
		r.retryAt = time.Time{}
		return note
	}

	r.failures++
	r.retryAt = r.now().Add(rotateRetryWindow)

	if r.degraded {
		return ""
	}
	r.degraded = true

	return r.note(fmt.Sprintf(
		"log rotation failed, continuing to append to the current file and retrying in %s: %v",
		rotateRetryWindow, err,
	))
}

// note formats a diagnostic the way hclog formats its own entries so it does
// not break tooling that parses the log file.
func (r *RotatingFile) note(message string) string {
	return HclogWarnLine(r.now(), r.source, message)
}
