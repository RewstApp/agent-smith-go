package utils

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
)

const (
	// rotateRetryWindow is how long RotatingFile keeps appending to the current
	// file after a rotation fails before it tries again. Rotation is attempted
	// on the write that would cross the size ceiling, so without a window every
	// subsequent line would re-attempt the rename - and on Windows the usual
	// failure (another process holding the file open, e.g. the diagnostic log
	// viewer) can persist for as long as that process runs.
	rotateRetryWindow = time.Minute

	// rotateNoteSource is the logger name the rotation diagnostics are written
	// under, matching the service logger so the lines parse like its own.
	rotateNoteSource = "agent_smith"
)

// RotatingFile is an append-only log file writer that caps its own on-disk
// footprint. When a write would carry the active file past maxBytes the file is
// rotated first: path becomes path.1, the previous path.1 becomes path.2, and so
// on up to path.maxFiles, which is discarded. The worst-case footprint is
// therefore (maxFiles+1) * maxBytes plus one write's overshoot.
//
// The writer owns the file handle and closes it before renaming. That ordering
// is what makes rotation work on Windows, where a file another handle has open
// cannot be renamed; the agent's own handle is the common one, and closing it
// first means the common case succeeds. A handle held by another process - the
// diagnostic mode log viewer, or the detached --update helper writing its
// output - still blocks the rename on Windows, and on Linux/macOS leaves that
// process reading or writing the now-rotated file. In both cases rotation
// failure is non-fatal: the current file is reopened and appending continues,
// one WARN line records the failure, another records the eventual recovery,
// and nothing is written in between, so a rotation that stays blocked cannot
// flood the file it is failing to rotate. This is the same "best effort,
// reported once per transition" posture the syslog forwarder takes.
//
// A line accepted by Write is never lost to rotation: rotation happens before
// the write, under the same lock, and the write then lands in whichever file is
// open afterwards. The size is re-read from the handle on every write rather
// than only tracked, so bytes appended through a separate handle (see
// OpenAppendHandle) still count toward the ceiling.
type RotatingFile struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	maxFiles int
	mode     os.FileMode

	f    *os.File
	size int64

	degraded bool
	failures int
	retryAt  time.Time

	// Injectable for tests: forcing a rename failure exercises the degraded
	// path without depending on platform-specific sharing semantics.
	rename func(oldpath, newpath string) error
	now    func() time.Time
}

// NewRotatingFile opens (creating if necessary) path for appending with the
// given mode and returns a writer that rotates it at maxBytes, keeping at most
// maxFiles rotated copies. Both bounds must be positive; callers resolve
// defaults before getting here (see agent.Device.ResolvedLogMaxBytes).
func NewRotatingFile(
	path string,
	maxBytes int64,
	maxFiles int,
	mode os.FileMode,
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
		rename:   os.Rename,
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
// counted toward the ceiling on the next Write here, because the size is
// re-read from the file rather than only tracked.
func (r *RotatingFile) OpenAppendHandle() (*os.File, error) {
	return os.OpenFile(r.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, r.mode)
}

// Write appends p to the active file, rotating first if the write would carry
// it past the size ceiling. The returned (n, err) describe the write itself;
// a rotation failure never surfaces here, only in the file as a WARN line.
func (r *RotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.f == nil {
		// A previous rotation lost the handle (reopen failed, e.g. disk full).
		// Keep trying on every write so logging resumes as soon as it can.
		if err := r.open(); err != nil {
			return 0, err
		}
	}

	r.refreshSize()

	var note string
	// size > 0 so a single write larger than the whole ceiling is written to
	// an empty file rather than rotating an empty file forever.
	if r.size > 0 && r.size+int64(len(p)) > r.maxBytes {
		note = r.rotateLocked()
	}

	if r.f == nil {
		return 0, errors.New("log file is not open")
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

func (r *RotatingFile) open() error {
	f, err := os.OpenFile(r.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, r.mode)
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
	return nil
}

func (r *RotatingFile) refreshSize() {
	if fi, err := r.f.Stat(); err == nil {
		r.size = fi.Size()
	}
}

// rotateLocked performs one rotation attempt (or skips it while suppressed
// after a failure) and returns the diagnostic to write into the file: one line
// when rotation starts failing, one when it recovers, nothing otherwise.
func (r *RotatingFile) rotateLocked() string {
	if r.degraded && r.now().Before(r.retryAt) {
		return ""
	}

	// Close before renaming so the rename can succeed on Windows.
	if r.f != nil {
		_ = r.f.Close()
		r.f = nil
	}

	err := r.shiftAndRename()

	// Reopen regardless of the outcome: after a successful rename this creates
	// the new active file; after a failed one it reopens the same file so
	// appending continues. If even that fails, r.f stays nil and Write reports
	// it, retrying on the next call.
	if reopenErr := r.open(); reopenErr != nil && err == nil {
		err = reopenErr
	}

	return r.recordResult(err)
}

// shiftAndRename discards the oldest rotated copy, shifts the rest up one slot,
// and moves the active file into slot 1. Only the final rename is load-bearing:
// a shift that fails just means one rotated copy is overwritten a step early,
// which costs history, not the active log.
func (r *RotatingFile) shiftAndRename() error {
	r.pruneBeyondMaxFiles()

	_ = os.Remove(r.backupPath(r.maxFiles))
	for i := r.maxFiles - 1; i >= 1; i-- {
		src := r.backupPath(i)
		if _, err := os.Lstat(src); err != nil {
			continue
		}
		_ = r.rename(src, r.backupPath(i+1))
	}

	if err := r.rename(r.path, r.backupPath(1)); err != nil {
		return fmt.Errorf("rotate %s: %w", r.path, err)
	}
	return nil
}

// pruneBeyondMaxFiles removes rotated copies numbered above maxFiles, so
// lowering log_max_files between runs shrinks the footprint instead of leaving
// the extra copies on disk indefinitely.
func (r *RotatingFile) pruneBeyondMaxFiles() {
	matches, err := filepath.Glob(r.path + ".*")
	if err != nil {
		return
	}
	prefix := r.path + "."
	for _, m := range matches {
		n, convErr := strconv.Atoi(strings.TrimPrefix(m, prefix))
		if convErr == nil && n > r.maxFiles {
			_ = os.Remove(m)
		}
	}
}

func (r *RotatingFile) backupPath(i int) string {
	return fmt.Sprintf("%s.%d", r.path, i)
}

// recordResult folds one rotation attempt into the health state and returns
// the diagnostic for the file, mirroring the syslog forwarder's once-per-
// transition reporting.
func (r *RotatingFile) recordResult(err error) string {
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
	return fmt.Sprintf(
		"%s [WARN]  %s: %s\n",
		r.now().Format(hclog.TimeFormat), rotateNoteSource, message,
	)
}
