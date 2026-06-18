package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/hashicorp/go-hclog"
)

const (
	// defaultSpoolMaxEntries bounds how many undelivered postbacks the on-disk
	// spool retains. Older entries are evicted once this many accumulate so a
	// prolonged engine outage cannot grow the spool without limit.
	defaultSpoolMaxEntries = 100
	// defaultSpoolMaxAge bounds how long an undelivered postback is retained.
	// Entries older than this are discarded on the next enqueue or flush; a stale
	// result is unlikely to be useful to a workflow that has long since timed out.
	defaultSpoolMaxAge = 24 * time.Hour
	// spoolFileSuffix is the extension used for spool entry files so unrelated
	// files in the directory are ignored.
	spoolFileSuffix = ".json"
)

// spoolEntry is the durable record of a command result whose postback could not
// be delivered in-line. It carries everything needed to rebuild and retry the
// postback on a later connection cycle.
type spoolEntry struct {
	PostId    string    `json:"post_id"`
	Result    []byte    `json:"result"`
	CreatedAt time.Time `json:"created_at"`
}

// postbackSpool is a bounded, file-backed queue of command results whose
// postback exhausted its in-line retry budget. Persisting them survives a
// transient engine outage (or an agent restart) so the result is re-attempted
// on a later cycle rather than lost.
//
// It is safe for concurrent use: command workers enqueue while a single flush
// goroutine drains. Disk mutations during enqueue (directory creation, capacity
// pruning, atomic file write) are serialized by mu, but flush deliberately does
// not hold mu while performing network delivery so a slow engine cannot stall
// workers — enqueue only ever creates new files and flush only ever reads or
// removes existing ones, so the two never corrupt a shared file.
type postbackSpool struct {
	dir        string
	maxEntries int
	maxAge     time.Duration
	logger     hclog.Logger

	mu  sync.Mutex
	seq uint64

	// droppedTotal counts spool entries discarded because the spool was at
	// capacity or an entry exceeded maxAge. Exposed for observability beyond the
	// per-drop log line.
	droppedTotal atomic.Int64
}

func newPostbackSpool(dir string, maxEntries int, maxAge time.Duration, logger hclog.Logger) *postbackSpool {
	if maxEntries <= 0 {
		maxEntries = defaultSpoolMaxEntries
	}
	if maxAge <= 0 {
		maxAge = defaultSpoolMaxAge
	}
	return &postbackSpool{
		dir:        dir,
		maxEntries: maxEntries,
		maxAge:     maxAge,
		logger:     logger,
	}
}

// enqueue persists entry for later delivery. The spool is kept within its
// configured size and age bounds: expired entries and, if necessary, the oldest
// entries are evicted before the new one is written. The write is atomic (temp
// file + rename) so a flush never observes a partially written entry.
func (s *postbackSpool) enqueue(entry spoolEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.dir, utils.DefaultDirMod); err != nil {
		return fmt.Errorf("create spool dir: %w", err)
	}

	// Drop expired entries first, then evict oldest until there is room for one
	// more (target maxEntries-1 so the new write lands at the cap).
	s.pruneLocked(s.maxEntries - 1)

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("marshal spool entry: %w", err)
	}

	s.seq++
	name := fmt.Sprintf("%020d-%06d%s", entry.CreatedAt.UnixNano(), s.seq, spoolFileSuffix)
	final := filepath.Join(s.dir, name)
	tmp := final + ".tmp"

	if err := os.WriteFile(tmp, data, utils.DefaultFileMod); err != nil {
		return fmt.Errorf("write spool entry: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("commit spool entry: %w", err)
	}
	return nil
}

// pruneLocked removes expired entries and then, if more than keep entries
// remain, evicts the oldest until keep remain. Callers must hold mu.
func (s *postbackSpool) pruneLocked(keep int) {
	files := s.listLocked()
	if len(files) == 0 {
		return
	}

	cutoff := time.Now().Add(-s.maxAge)
	survivors := files[:0]
	for _, name := range files {
		if ts, ok := spoolFileTime(name); ok && ts.Before(cutoff) {
			s.removeLocked(name, "expired")
			continue
		}
		survivors = append(survivors, name)
	}

	if keep < 0 {
		keep = 0
	}
	for len(survivors) > keep {
		s.removeLocked(survivors[0], "capacity")
		survivors = survivors[1:]
	}
}

// listLocked returns the spool entry file names sorted oldest-first. The
// timestamp-prefixed names sort chronologically by lexical order. Callers must
// hold mu.
func (s *postbackSpool) listLocked() []string {
	dirEntries, err := os.ReadDir(s.dir)
	if err != nil {
		if !os.IsNotExist(err) {
			s.logger.Error("Failed to read postback spool dir", "dir", s.dir, "error", err)
		}
		return nil
	}

	names := make([]string, 0, len(dirEntries))
	for _, de := range dirEntries {
		if de.IsDir() || filepath.Ext(de.Name()) != spoolFileSuffix {
			continue
		}
		names = append(names, de.Name())
	}
	sort.Strings(names)
	return names
}

func (s *postbackSpool) removeLocked(name, reason string) {
	if err := os.Remove(filepath.Join(s.dir, name)); err != nil && !os.IsNotExist(err) {
		s.logger.Error("Failed to remove spool entry", "file", name, "reason", reason, "error", err)
		return
	}
	dropped := s.droppedTotal.Add(1)
	s.logger.Error(
		"Postback spool entry dropped",
		"file", name,
		"reason", reason,
		"dropped_total", dropped,
	)
}

// flush attempts to deliver each spooled entry oldest-first. deliver reports
// done=true when the entry is resolved (delivered or permanently rejected), in
// which case it is removed from the spool. A done=false result is treated as a
// transient failure (the engine is still unreachable): flushing stops so the
// remaining entries are retried on a later cycle, preserving order. Flushing
// also stops promptly when ctx is cancelled so it never delays teardown.
func (s *postbackSpool) flush(ctx context.Context, deliver func(spoolEntry) (bool, error)) {
	s.mu.Lock()
	names := s.listLocked()
	s.mu.Unlock()

	if len(names) == 0 {
		return
	}

	cutoff := time.Now().Add(-s.maxAge)
	delivered := 0
	for _, name := range names {
		if ctx.Err() != nil {
			return
		}

		path := filepath.Join(s.dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				s.logger.Error("Failed to read spool entry", "file", name, "error", err)
			}
			continue
		}

		var entry spoolEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			// A corrupt entry can never be delivered; drop it rather than wedging
			// the flush on it forever.
			s.logger.Error("Discarding corrupt spool entry", "file", name, "error", err)
			s.remove(name)
			continue
		}

		if entry.CreatedAt.Before(cutoff) {
			s.remove(name)
			continue
		}

		done, derr := deliver(entry)
		if !done {
			// Engine still unreachable — stop and retry the rest next cycle.
			if derr != nil {
				s.logger.Info(
					"Postback spool flush paused: engine still unreachable",
					"post_id", entry.PostId,
					"delivered", delivered,
					"error", derr,
				)
			}
			return
		}
		s.remove(name)
		delivered++
	}

	if delivered > 0 {
		s.logger.Info("Postback spool flushed", "delivered", delivered)
	}
}

// remove deletes a spool entry that has been resolved during flush.
func (s *postbackSpool) remove(name string) {
	if err := os.Remove(filepath.Join(s.dir, name)); err != nil && !os.IsNotExist(err) {
		s.logger.Error("Failed to remove spool entry", "file", name, "error", err)
	}
}

// spoolFileTime parses the creation timestamp encoded in a spool file name
// (the leading zero-padded unix-nano field). It returns ok=false for names that
// do not match the expected format.
func spoolFileTime(name string) (time.Time, bool) {
	base := name[:len(name)-len(spoolFileSuffix)]
	var nano, seq uint64
	if _, err := fmt.Sscanf(base, "%020d-%06d", &nano, &seq); err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, int64(nano)), true
}
