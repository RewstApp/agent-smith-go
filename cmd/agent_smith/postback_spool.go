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
	// defaultSpoolMaxAttempts bounds how many flush cycles may reject a single
	// entry at the HTTP layer before it is abandoned. An entry the engine keeps
	// rejecting is undeliverable, and retrying it forever is what used to strand
	// every entry behind it until they aged out. The bound is deliberately
	// generous: attempts are consumed at most once per connection cycle, so five
	// of them span a long stretch of wall clock and a rejection that is genuinely
	// transient has ample room to clear. Only HTTP-level rejections count against
	// it — a connectivity failure is not the entry's fault and consumes nothing.
	defaultSpoolMaxAttempts = 5
	// minAttemptInterval is the minimum wall-clock spacing between two rejections
	// that both count against an entry's attempt budget.
	//
	// It exists because "the engine rejected this entry" is inferred from an HTTP
	// answer, and that inference is weakest in the one case where it matters: an
	// engine that is failing wholesale answers 5xx for every entry, which reads as
	// a per-entry rejection of each of them. Reconnects can be minutes apart when
	// the connection is flapping, so without spacing a broken engine could burn a
	// whole budget in a few minutes and abandon results it would have accepted on
	// recovery — worse than the behaviour this change replaces. Spacing bounds the
	// budget in time rather than in cycles: results survive at least
	// (maxAttempts-1) * this interval of a wholesale outage, however often the
	// agent reconnects. It does not slow the fix down — a rejected entry is still
	// passed over immediately, every cycle; only the counting is spaced.
	minAttemptInterval = 10 * time.Minute
	// spoolFileSuffix is the extension used for spool entry files so unrelated
	// files in the directory are ignored.
	spoolFileSuffix = ".json"
)

// Reasons an entry leaves the spool without being delivered. They are distinct
// strings because they describe very different failures and are counted
// separately: expired and capacity are pressure on a spool that is doing its
// job, while attempts_exhausted is a result the engine would not take.
const (
	dropReasonExpired  = "expired"
	dropReasonCapacity = "capacity"
	dropReasonAttempts = "attempts_exhausted"
	dropReasonCorrupt  = "corrupt"
)

// deliveryOutcome classifies the result of one delivery attempt. The
// distinction between the two failure outcomes is the point: the flush must
// tell "the engine is unreachable" (nothing behind this entry can be delivered
// either) apart from "the engine rejected this entry" (everything behind it
// would still be delivered).
type deliveryOutcome int

const (
	// deliveryDone means the entry needs no further attempts — it was accepted,
	// already fulfilled, or permanently rejected. The entry is removed.
	deliveryDone deliveryOutcome = iota
	// deliveryRetryEntry means the engine answered but would not take this entry
	// (a 5xx, or a response that could not be parsed). The engine is plainly
	// reachable, so the flush counts an attempt against this entry and moves on
	// to the next one.
	deliveryRetryEntry
	// deliveryUnreachable means the attempt never got an answer: a transport
	// error, or a connection that broke while the response was being read. The
	// flush stops, because attempting the rest would only repeat the failure.
	deliveryUnreachable
)

// spoolEntry is the durable record of a command result whose postback could not
// be delivered in-line. It carries everything needed to rebuild and retry the
// postback on a later connection cycle.
//
// Attempts and LastError are persisted with the entry so an agent restart does
// not reset an undeliverable entry's budget and start the cycle over. Both are
// omitempty, and an entry file written by an older agent simply unmarshals with
// Attempts zero — it is treated as never attempted rather than discarded.
type spoolEntry struct {
	PostId    string    `json:"post_id"`
	Result    []byte    `json:"result"`
	CreatedAt time.Time `json:"created_at"`
	// Attempts counts HTTP-level rejections of this entry across flush cycles.
	// Connectivity failures are deliberately not counted.
	Attempts int `json:"attempts,omitempty"`
	// LastError records why the most recent attempt was rejected, so the log line
	// that eventually abandons the entry can say what the engine kept objecting to.
	LastError string `json:"last_error,omitempty"`
	// LastAttemptAt is when the most recent rejection was counted, used to space
	// out attempts (see minAttemptInterval). Zero on an entry that has never been
	// rejected, including one written by an older agent.
	LastAttemptAt time.Time `json:"last_attempt_at,omitempty"`
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
	dir         string
	maxEntries  int
	maxAge      time.Duration
	maxAttempts int
	// attemptInterval is the minimum spacing between counted attempts; see
	// minAttemptInterval. Configurable so tests need not wait it out.
	attemptInterval time.Duration
	logger          hclog.Logger

	mu  sync.Mutex
	seq uint64

	// droppedTotal counts every spool entry discarded without being delivered,
	// for whatever reason. The per-reason counters below break that total down:
	// a spool shedding entries because it is full or because they went stale is
	// a different condition from one abandoning a result the engine refuses, and
	// a single number cannot tell an operator which is happening.
	droppedTotal atomic.Int64

	droppedExpired  atomic.Int64
	droppedCapacity atomic.Int64
	droppedAttempts atomic.Int64
	droppedCorrupt  atomic.Int64
}

func newPostbackSpool(
	dir string,
	maxEntries int,
	maxAge time.Duration,
	maxAttempts int,
	logger hclog.Logger,
) *postbackSpool {
	if maxEntries <= 0 {
		maxEntries = defaultSpoolMaxEntries
	}
	if maxAge <= 0 {
		maxAge = defaultSpoolMaxAge
	}
	if maxAttempts <= 0 {
		maxAttempts = defaultSpoolMaxAttempts
	}
	return &postbackSpool{
		dir:             dir,
		maxEntries:      maxEntries,
		maxAge:          maxAge,
		maxAttempts:     maxAttempts,
		attemptInterval: minAttemptInterval,
		logger:          logger,
	}
}

// countDrop records a discarded entry against the total and its reason.
func (s *postbackSpool) countDrop(reason string) int64 {
	switch reason {
	case dropReasonExpired:
		s.droppedExpired.Add(1)
	case dropReasonCapacity:
		s.droppedCapacity.Add(1)
	case dropReasonAttempts:
		s.droppedAttempts.Add(1)
	case dropReasonCorrupt:
		s.droppedCorrupt.Add(1)
	}
	return s.droppedTotal.Add(1)
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
	s.drop(name, reason)
}

// drop deletes an entry that is being discarded undelivered, counting it against
// the total and its reason and logging why. extra carries reason-specific detail
// (attempt count, last error) so the log line is self-explanatory. It takes no
// lock, so it is callable both from the locked prune path and from flush.
func (s *postbackSpool) drop(name, reason string, extra ...any) {
	if err := os.Remove(filepath.Join(s.dir, name)); err != nil && !os.IsNotExist(err) {
		s.logger.Error("Failed to remove spool entry", "file", name, "reason", reason, "error", err)
		return
	}
	dropped := s.countDrop(reason)
	args := []any{"file", name, "reason", reason, "dropped_total", dropped}
	s.logger.Error("Postback spool entry dropped", append(args, extra...)...)
}

// flush attempts to deliver each spooled entry oldest-first, and its handling of
// the two failure outcomes is the whole point of this function.
//
// deliveryDone removes the entry — it was accepted, already fulfilled, or
// permanently rejected, so there is nothing left to retry.
//
// deliveryUnreachable stops the flush and leaves every remaining entry on disk
// for a later cycle. Nothing behind this entry could be delivered either, so
// attempting the rest would only repeat the failure and waste the cycle.
//
// deliveryRetryEntry does NOT stop the flush. The engine answered — it simply
// would not take this entry — so every entry behind it may well be deliverable
// and is attempted. The rejected entry keeps its place in the queue with its
// attempt count incremented, and once it exhausts maxAttempts it is abandoned
// with its own drop reason and onAbandon is invoked so the loss is surfaced
// beyond the log. Before this distinction existed, any HTTP-level rejection was
// read as "the engine is unreachable": one entry the engine consistently 5xx'd
// pinned the queue, was retried forever with no attempt bound, and the healthy
// results behind it were eventually aged out and reported as merely stale.
//
// onAbandon may be nil. Flushing stops promptly when ctx is cancelled so it
// never delays teardown.
func (s *postbackSpool) flush(
	ctx context.Context,
	deliver func(spoolEntry) (deliveryOutcome, error),
	onAbandon func(entry spoolEntry, err error),
) {
	s.mu.Lock()
	names := s.listLocked()
	s.mu.Unlock()

	if len(names) == 0 {
		return
	}

	cutoff := time.Now().Add(-s.maxAge)
	delivered := 0
	abandoned := 0
	rejected := 0
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
			s.drop(name, dropReasonCorrupt)
			continue
		}

		if entry.CreatedAt.Before(cutoff) {
			s.drop(name, dropReasonExpired, "post_id", entry.PostId, "created_at", entry.CreatedAt)
			continue
		}

		outcome, derr := deliver(entry)
		switch outcome {
		case deliveryDone:
			s.remove(name)
			delivered++

		case deliveryUnreachable:
			// Nothing behind this entry is deliverable either. Stop without
			// charging an attempt: the engine being down is not this entry's fault,
			// and counting it would burn an undeliverable entry's whole budget on a
			// single outage.
			s.logger.Info(
				"Postback spool flush paused: engine unreachable",
				"post_id", entry.PostId,
				"delivered", delivered,
				"error", derr,
			)
			return

		case deliveryRetryEntry:
			rejected++
			if derr != nil {
				entry.LastError = derr.Error()
			}

			// Pass over the entry either way — that is the fix, and it is not
			// conditional. Only whether this rejection is COUNTED is spaced out, so
			// an engine failing wholesale cannot spend an entry's whole budget in a
			// burst of quick reconnects.
			now := time.Now()
			if !entry.LastAttemptAt.IsZero() &&
				now.Sub(entry.LastAttemptAt) < s.attemptInterval {
				s.recordAttempt(name, entry)
				continue
			}
			entry.Attempts++
			entry.LastAttemptAt = now

			if entry.Attempts >= s.maxAttempts {
				s.drop(
					name, dropReasonAttempts,
					"post_id", entry.PostId,
					"attempts", entry.Attempts,
					"last_error", entry.LastError,
				)
				abandoned++
				if onAbandon != nil {
					onAbandon(entry, derr)
				}
				continue
			}
			s.recordAttempt(name, entry)
		}
	}

	if delivered > 0 || rejected > 0 {
		s.logger.Info(
			"Postback spool flushed",
			"delivered", delivered,
			"rejected", rejected,
			"abandoned", abandoned,
		)
	}
}

// recordAttempt persists an entry whose delivery was rejected, so its attempt
// count and last error survive both the rest of this flush and an agent
// restart. The file keeps its name, so the entry keeps its place in the
// oldest-first order.
//
// The rewrite is atomic (temp file + rename) and holds mu, which is what makes
// it safe against a concurrent enqueue pruning this very entry: prune runs under
// the same lock, so the existence check below cannot race it. Without that check
// a rewrite could resurrect an entry that prune had just evicted.
func (s *postbackSpool) recordAttempt(name string, entry spoolEntry) {
	data, err := json.Marshal(entry)
	if err != nil {
		s.logger.Error("Failed to marshal spool entry attempt", "file", name, "error", err)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	final := filepath.Join(s.dir, name)
	if _, statErr := os.Stat(final); statErr != nil {
		// Evicted while we were delivering. Leave it evicted.
		return
	}

	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, data, utils.DefaultFileMod); err != nil {
		s.logger.Error("Failed to write spool entry attempt", "file", name, "error", err)
		return
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		s.logger.Error("Failed to commit spool entry attempt", "file", name, "error", err)
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
