package utils

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Compression of rotated log copies (sc-132996). Agent logs compress about
// 10:1, so with log_compress on an operator can keep far more history in the
// same footprint: rewst_agent.log.N becomes rewst_agent.log.N.gz shortly after
// it is rotated. Off by default, which leaves the files on disk exactly as
// before.

const (
	// gzipSuffix names a compressed rotated copy: path.N.gz.
	gzipSuffix = ".gz"
	// gzipTempSuffix names a copy being compressed. A file left behind by a
	// process that exited mid-compression is removed on the next pass.
	gzipTempSuffix = ".gz.tmp"
)

// SetCompress turns compression of rotated copies on or off, at once: the
// device twin can change it while the service runs. Turning it on also
// compresses the plain copies already on disk. Turning it off leaves the
// compressed copies where they are (they keep shifting and ageing out like
// any other copy); new rotations are then left plain.
func (r *RotatingFile) SetCompress(on bool) {
	r.compress.Store(on)
	if !on {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.kickCompressLocked()
}

// CompressEnabled reports whether rotated copies are being compressed.
func (r *RotatingFile) CompressEnabled() bool {
	return r.compress.Load()
}

// kickCompressLocked asks for a compression pass, starting the worker when it
// is not running. The pass itself never runs under the writer's lock: only
// the final rename that swaps a copy for its compressed form does, and that is
// a metadata operation like the rotation itself, so logging is never held up
// by the gzip.
func (r *RotatingFile) kickCompressLocked() {
	r.compressPending = true
	if r.compressing {
		return
	}
	r.compressing = true
	r.compressWG.Add(1)
	go r.compressLoop()
}

// compressLoop runs passes until none is pending, then exits; the next
// rotation starts it again.
func (r *RotatingFile) compressLoop() {
	defer r.compressWG.Done()
	for {
		r.mu.Lock()
		if !r.compressPending || !r.compress.Load() {
			r.compressing = false
			r.mu.Unlock()
			return
		}
		r.compressPending = false
		r.mu.Unlock()

		if retry := r.compressPass(); retry {
			// A copy changed under the pass (rotated again, or still being
			// appended to through the --update helper's inherited handle):
			// give it a moment rather than spinning on it.
			time.Sleep(r.compressRetryDelay)
			r.mu.Lock()
			r.compressPending = true
			r.mu.Unlock()
		}
	}
}

// compressPass compresses every plain rotated copy, lowest slot first, and
// reports whether one changed under it and needs another pass. A copy that
// fails to compress stays plain and is not retried until the next rotation or
// SetCompress(true), so a persistent failure cannot spin.
func (r *RotatingFile) compressPass() (retry bool) {
	r.removeStaleTemps()
	copies := r.existingBackups()
	indices := make([]int, 0, len(copies))
	for i, c := range copies {
		if c.plain {
			indices = append(indices, i)
		}
	}
	sort.Ints(indices)
	for _, i := range indices {
		if !r.compress.Load() {
			return false
		}
		if r.compressOne(i) {
			retry = true
		}
	}
	return retry
}

// removeStaleTemps deletes compression temp files: the worker is the only
// writer of them and runs one pass at a time, so at the start of a pass any
// that exist were left by an earlier process.
func (r *RotatingFile) removeStaleTemps() {
	entries, err := os.ReadDir(filepath.Dir(r.path))
	if err != nil {
		return
	}
	prefix := filepath.Base(r.path) + "."
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() && strings.HasPrefix(name, prefix) &&
			strings.HasSuffix(name, gzipTempSuffix) {
			_ = os.Remove(filepath.Join(filepath.Dir(r.path), name))
		}
	}
}

// compressOne gzips slot i's plain copy beside it and, under the writer's
// lock, swaps it in: only when the plain copy is still the same file, the same
// size and the same age as when the gzip started, so a copy that was rotated
// to another slot meanwhile, or appended to, is never replaced by a stale
// compressed form. It reports whether it should be tried again.
func (r *RotatingFile) compressOne(i int) (retry bool) {
	src := r.backupPath(i)
	before, err := os.Stat(src)
	if err != nil {
		// Rotated away or aged out since the listing: the rotation that moved
		// it asked for another pass, which sees it where it is now.
		return false
	}
	tmp := src + gzipTempSuffix
	if err := r.gzipFile(src, tmp); err != nil {
		_ = os.Remove(tmp)
		r.mu.Lock()
		r.recordCompressResultLocked(src, err)
		r.mu.Unlock()
		return false
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	after, err := os.Stat(src)
	if err != nil || !os.SameFile(before, after) || after.Size() != before.Size() ||
		!after.ModTime().Equal(before.ModTime()) {
		_ = os.Remove(tmp)
		return true
	}
	dst := r.gzipPath(i)
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		r.recordCompressResultLocked(src, err)
		return false
	}
	if err := os.Remove(src); err != nil {
		// Never leave both forms of one slot: the plain copy is the one that
		// is certainly whole, so the compressed one goes.
		_ = os.Remove(dst)
		r.recordCompressResultLocked(src, err)
		return false
	}
	r.recordCompressResultLocked(src, nil)
	return false
}

// gzipFile writes src compressed to dst and syncs it, so the rename that
// follows never exposes a partial file.
func gzipFile(src, dst string) error {
	in, err := OpenSharedRead(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	zw := gzip.NewWriter(out)
	if _, err := io.Copy(zw, in); err != nil {
		_ = zw.Close()
		_ = out.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// recordCompressResultLocked reports compression once per transition, the way
// rotation failures are: one WARN when it starts failing (the plain copy is
// kept), one when it works again.
func (r *RotatingFile) recordCompressResultLocked(src string, err error) {
	if err == nil {
		if r.compressFailing {
			r.compressFailing = false
			r.owedNote += r.note("log compression recovered")
		}
		r.flushOwedNoteLocked()
		return
	}
	if r.compressFailing {
		return
	}
	r.compressFailing = true
	r.owedNote += r.note(fmt.Sprintf(
		"log compression failed, keeping the plain rotated copy %s: %v",
		filepath.Base(src), err,
	))
	r.flushOwedNoteLocked()
}

// flushOwedNoteLocked writes a compression note into the active file when it
// is open and at a line boundary, so the note never lands inside a line a
// writer is still delivering in pieces; otherwise the next Write tries again.
func (r *RotatingFile) flushOwedNoteLocked() {
	if r.owedNote == "" || r.f == nil || !r.atLineStart {
		return
	}
	if k, err := r.f.WriteString(r.owedNote); err == nil {
		r.size += int64(k)
	}
	r.owedNote = ""
}

// waitCompression waits for the compression worker to go idle. Tests only.
func (r *RotatingFile) waitCompression() {
	r.compressWG.Wait()
}
