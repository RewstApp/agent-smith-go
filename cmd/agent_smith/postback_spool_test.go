package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/hashicorp/go-hclog"
)

func newTestSpool(t *testing.T, maxEntries int, maxAge time.Duration) *postbackSpool {
	t.Helper()
	return newTestSpoolWithAttempts(t, maxEntries, maxAge, defaultSpoolMaxAttempts)
}

func newTestSpoolWithAttempts(
	t *testing.T,
	maxEntries int,
	maxAge time.Duration,
	maxAttempts int,
) *postbackSpool {
	t.Helper()
	s := newPostbackSpool(t.TempDir(), maxEntries, maxAge, maxAttempts, hclog.NewNullLogger())
	// Count every rejection: the production spacing exists to survive a wholesale
	// outage across fast reconnects, and tests that drive a budget to exhaustion
	// would otherwise have to wait it out. Its own behaviour is covered by
	// TestSpool_RapidRejectionsDoNotBurnTheAttemptBudget.
	s.attemptInterval = 0
	return s
}

func countSpoolFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read spool dir: %v", err)
	}
	n := 0
	for _, e := range entries {
		if filepath.Ext(e.Name()) == spoolFileSuffix {
			n++
		}
	}
	return n
}

// TestSpool_EnqueueFlushRoundTrip verifies that spooled entries are delivered
// oldest-first and removed from disk once delivered.
func TestSpool_EnqueueFlushRoundTrip(t *testing.T) {
	s := newTestSpool(t, 10, time.Hour)

	for _, id := range []string{"a", "b", "c"} {
		if err := s.enqueue(
			spoolEntry{PostId: id, Result: []byte(id), CreatedAt: time.Now()},
		); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
	}

	var delivered []string
	s.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
		delivered = append(delivered, e.PostId)
		return deliveryDone, nil
	}, nil)

	want := []string{"a", "b", "c"}
	if len(delivered) != len(want) {
		t.Fatalf("expected %d deliveries, got %v", len(want), delivered)
	}
	for i := range want {
		if delivered[i] != want[i] {
			t.Errorf("delivery order mismatch at %d: got %q want %q", i, delivered[i], want[i])
		}
	}
	if n := countSpoolFiles(t, s.dir); n != 0 {
		t.Errorf("expected spool emptied after delivery, %d files remain", n)
	}
}

// TestSpool_FlushStopsWhenEngineUnreachable verifies that a connectivity
// failure halts the flush and leaves every remaining entry on disk for a later
// cycle, preserving order. This optimization predates sc-106112 and must
// survive it: when nothing can be delivered, attempting the rest is pointless.
func TestSpool_FlushStopsWhenEngineUnreachable(t *testing.T) {
	s := newTestSpool(t, 10, time.Hour)

	for _, id := range []string{"a", "b", "c"} {
		if err := s.enqueue(
			spoolEntry{PostId: id, Result: []byte(id), CreatedAt: time.Now()},
		); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
	}

	var attempts int
	s.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
		attempts++
		return deliveryUnreachable, context.DeadlineExceeded // engine down
	}, nil)

	if attempts != 1 {
		t.Errorf("expected flush to stop after first transient failure, got %d attempts", attempts)
	}
	if n := countSpoolFiles(t, s.dir); n != 3 {
		t.Errorf("expected all 3 entries retained, %d remain", n)
	}
}

// TestSpool_CapacityBound verifies that the spool never exceeds maxEntries: the
// oldest entries are evicted as new ones arrive.
func TestSpool_CapacityBound(t *testing.T) {
	s := newTestSpool(t, 3, time.Hour)

	for _, id := range []string{"a", "b", "c", "d", "e"} {
		if err := s.enqueue(
			spoolEntry{PostId: id, Result: []byte(id), CreatedAt: time.Now()},
		); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
		// Distinct timestamps keep filename ordering deterministic.
		time.Sleep(time.Millisecond)
	}

	if n := countSpoolFiles(t, s.dir); n != 3 {
		t.Fatalf("expected spool capped at 3, got %d", n)
	}
	if got := s.droppedTotal.Load(); got != 2 {
		t.Errorf("expected 2 capacity drops, got %d", got)
	}

	var delivered []string
	s.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
		delivered = append(delivered, e.PostId)
		return deliveryDone, nil
	}, nil)
	want := []string{"c", "d", "e"}
	if len(delivered) != 3 {
		t.Fatalf("expected 3 survivors, got %v", delivered)
	}
	for i := range want {
		if delivered[i] != want[i] {
			t.Errorf(
				"expected newest entries retained; at %d got %q want %q",
				i,
				delivered[i],
				want[i],
			)
		}
	}
}

// TestSpool_AgeBoundOnFlush verifies that entries older than maxAge are
// discarded during flush without being delivered.
func TestSpool_AgeBoundOnFlush(t *testing.T) {
	s := newTestSpool(t, 10, 10*time.Millisecond)

	if err := s.enqueue(
		spoolEntry{PostId: "old", Result: []byte("x"), CreatedAt: time.Now()},
	); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	time.Sleep(30 * time.Millisecond)

	var attempts int
	s.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
		attempts++
		return deliveryDone, nil
	}, nil)

	if attempts != 0 {
		t.Errorf("expected expired entry not to be delivered, got %d attempts", attempts)
	}
	if n := countSpoolFiles(t, s.dir); n != 0 {
		t.Errorf("expected expired entry removed, %d remain", n)
	}
}

// TestSpool_AgeBoundOnEnqueue verifies that a later enqueue prunes entries that
// have aged out, bounding growth even without a flush.
//
// Age is decided by comparing an entry's CreatedAt against now-maxAge, so the
// two entries are separated by backdating the old one rather than by sleeping
// past a short maxAge. A sleep-based version has to pick a maxAge small enough to
// keep the test quick, which then also has to outlast the directory read and
// flush that follow - on a slow or loaded runner the fresh entry ages out too and
// the flush delivers nothing. Backdating keeps maxAge comfortably longer than the
// whole test while still putting the old entry unambiguously past it.
func TestSpool_AgeBoundOnEnqueue(t *testing.T) {
	s := newTestSpool(t, 10, time.Minute)

	if err := s.enqueue(
		spoolEntry{PostId: "old", Result: []byte("x"), CreatedAt: time.Now().Add(-time.Hour)},
	); err != nil {
		t.Fatalf("enqueue old: %v", err)
	}
	if err := s.enqueue(
		spoolEntry{PostId: "new", Result: []byte("y"), CreatedAt: time.Now()},
	); err != nil {
		t.Fatalf("enqueue new: %v", err)
	}

	if n := countSpoolFiles(t, s.dir); n != 1 {
		t.Fatalf("expected expired entry pruned on enqueue, %d files remain", n)
	}

	var delivered []string
	s.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
		delivered = append(delivered, e.PostId)
		return deliveryDone, nil
	}, nil)
	if len(delivered) != 1 || delivered[0] != "new" {
		t.Errorf("expected only the fresh entry to survive, got %v", delivered)
	}
}

// TestSpool_CorruptEntryDropped verifies a non-parseable spool file is discarded
// rather than wedging the flush.
func TestSpool_CorruptEntryDropped(t *testing.T) {
	s := newTestSpool(t, 10, time.Hour)
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A valid-looking name but garbage contents.
	bad := filepath.Join(s.dir, "00000000000000000001-000001"+spoolFileSuffix)
	if err := os.WriteFile(bad, []byte("not json"), 0o644); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	var attempts int
	s.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
		attempts++
		return deliveryDone, nil
	}, nil)

	if attempts != 0 {
		t.Errorf("expected corrupt entry not to be delivered, got %d attempts", attempts)
	}
	if n := countSpoolFiles(t, s.dir); n != 0 {
		t.Errorf("expected corrupt entry removed, %d remain", n)
	}
}

// TestSpool_FlushContextCancelled verifies that a cancelled context stops the
// flush before any delivery, so teardown is never delayed.
func TestSpool_FlushContextCancelled(t *testing.T) {
	s := newTestSpool(t, 10, time.Hour)
	if err := s.enqueue(
		spoolEntry{PostId: "a", Result: []byte("a"), CreatedAt: time.Now()},
	); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var attempts int
	s.flush(ctx, func(e spoolEntry) (deliveryOutcome, error) {
		attempts++
		return deliveryDone, nil
	}, nil)

	if attempts != 0 {
		t.Errorf("expected no delivery on cancelled context, got %d", attempts)
	}
	if n := countSpoolFiles(t, s.dir); n != 1 {
		t.Errorf("expected entry retained for a later cycle, %d remain", n)
	}
}

// TestSpool_FlushEmptyNoop verifies flushing an empty/absent spool is a no-op.
func TestSpool_FlushEmptyNoop(t *testing.T) {
	s := newTestSpool(t, 10, time.Hour)
	var attempts int
	s.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
		attempts++
		return deliveryDone, nil
	}, nil)
	if attempts != 0 {
		t.Errorf("expected no deliveries from empty spool, got %d", attempts)
	}
}

// TestSpool_ResultPreserved verifies the result payload round-trips intact.
func TestSpool_ResultPreserved(t *testing.T) {
	s := newTestSpool(t, 10, time.Hour)
	payload := []byte(`{"status":"ok","data":[1,2,3]}`)
	if err := s.enqueue(
		spoolEntry{PostId: "id:1", Result: payload, CreatedAt: time.Now()},
	); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	var got []byte
	s.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
		got = e.Result
		return deliveryDone, nil
	}, nil)
	if string(got) != string(payload) {
		t.Errorf("result payload corrupted: got %q want %q", got, payload)
	}
}

// TestSpool_PoisonedEntryDoesNotBlockHealthyEntries is the sc-106112 regression
// test. One entry the engine persistently rejects at the HTTP layer used to pin
// the whole queue: the flush read the rejection as "engine unreachable", stopped,
// and restarted from the same entry on every cycle, so the healthy results
// behind it were never attempted and were eventually aged out and reported as
// merely stale. The engine is plainly up here — it answers for every other
// post_id — so everything behind the poisoned entry must be delivered.
func TestSpool_PoisonedEntryDoesNotBlockHealthyEntries(t *testing.T) {
	s := newTestSpool(t, 10, time.Hour)

	for _, id := range []string{"poison", "b", "c"} {
		if err := s.enqueue(
			spoolEntry{PostId: id, Result: []byte(id), CreatedAt: time.Now()},
		); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
		// Distinct timestamps keep filename ordering deterministic, so "poison"
		// really is attempted first and really is in front of the others.
		time.Sleep(time.Millisecond)
	}

	var attempted []string
	s.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
		attempted = append(attempted, e.PostId)
		if e.PostId == "poison" {
			return deliveryRetryEntry, fmt.Errorf("postback failed: status 503: ")
		}
		return deliveryDone, nil
	}, nil)

	want := []string{"poison", "b", "c"}
	if len(attempted) != len(want) {
		t.Fatalf("expected every entry attempted in one flush, got %v", attempted)
	}
	for i := range want {
		if attempted[i] != want[i] {
			t.Errorf("attempt order mismatch at %d: got %q want %q", i, attempted[i], want[i])
		}
	}

	// The healthy entries are gone (delivered); the poisoned one is retained for
	// another cycle rather than dropped on its first rejection.
	if n := countSpoolFiles(t, s.dir); n != 1 {
		t.Errorf("expected only the poisoned entry retained, %d files remain", n)
	}
	if got := s.droppedTotal.Load(); got != 0 {
		t.Errorf("expected no drops on a single rejection, got %d", got)
	}
}

// A rejected entry keeps its attempt count across flushes, and is abandoned once
// the budget is spent — with its own drop reason, its own counter, and a
// callback so the loss is surfaced beyond the log. Ageing it out and calling it
// "expired" is exactly the misreport sc-106112 exists to remove.
func TestSpool_PoisonedEntryAbandonedAfterMaxAttempts(t *testing.T) {
	const maxAttempts = 3
	s := newTestSpoolWithAttempts(t, 10, time.Hour, maxAttempts)

	if err := s.enqueue(
		spoolEntry{PostId: "poison", Result: []byte("x"), CreatedAt: time.Now()},
	); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	var abandoned []spoolEntry
	var seenAttempts []int
	reject := func(e spoolEntry) (deliveryOutcome, error) {
		seenAttempts = append(seenAttempts, e.Attempts)
		return deliveryRetryEntry, fmt.Errorf("postback failed: status 503: ")
	}
	onAbandon := func(e spoolEntry, err error) { abandoned = append(abandoned, e) }

	// One flush per connection cycle; the entry survives until its budget is out.
	for cycle := 1; cycle <= maxAttempts; cycle++ {
		s.flush(context.Background(), reject, onAbandon)

		remaining := countSpoolFiles(t, s.dir)
		if cycle < maxAttempts && remaining != 1 {
			t.Fatalf("cycle %d: expected the entry retained, %d files remain", cycle, remaining)
		}
		if cycle == maxAttempts && remaining != 0 {
			t.Errorf("cycle %d: expected the entry abandoned, %d files remain", cycle, remaining)
		}
	}

	// The attempt count the deliverer saw grew across cycles: it is persisted with
	// the entry, not recomputed per flush.
	want := []int{0, 1, 2}
	if len(seenAttempts) != len(want) {
		t.Fatalf("expected %d attempts, got %v", len(want), seenAttempts)
	}
	for i := range want {
		if seenAttempts[i] != want[i] {
			t.Errorf("attempt %d: entry carried Attempts=%d, want %d", i, seenAttempts[i], want[i])
		}
	}

	if len(abandoned) != 1 {
		t.Fatalf("expected exactly one abandonment callback, got %d", len(abandoned))
	}
	if abandoned[0].PostId != "poison" {
		t.Errorf("abandoned the wrong entry: %q", abandoned[0].PostId)
	}
	if abandoned[0].Attempts != maxAttempts {
		t.Errorf(
			"expected Attempts=%d on the abandoned entry, got %d",
			maxAttempts,
			abandoned[0].Attempts,
		)
	}
	if abandoned[0].LastError == "" {
		t.Error("expected the abandoned entry to carry the last error it was rejected with")
	}

	// The drop is counted under its own reason, not lumped in with stale-data
	// cleanup or capacity pressure — the two are diagnosed very differently.
	if got := s.droppedAttempts.Load(); got != 1 {
		t.Errorf("expected 1 attempts-exhausted drop, got %d", got)
	}
	if got := s.droppedExpired.Load(); got != 0 {
		t.Errorf("an abandoned entry must not be counted as expired, got %d", got)
	}
	if got := s.droppedCapacity.Load(); got != 0 {
		t.Errorf("an abandoned entry must not be counted as capacity, got %d", got)
	}
	if got := s.droppedTotal.Load(); got != 1 {
		t.Errorf("expected the drop counted once in the total, got %d", got)
	}
}

// A connectivity failure is not the entry's fault: it must not consume the
// entry's attempt budget, or a single long outage would burn through it and
// abandon results the engine never actually refused.
func TestSpool_UnreachableEngineDoesNotConsumeAttempts(t *testing.T) {
	s := newTestSpoolWithAttempts(t, 10, time.Hour, 2)

	if err := s.enqueue(
		spoolEntry{PostId: "a", Result: []byte("a"), CreatedAt: time.Now()},
	); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	for range 5 {
		s.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
			if e.Attempts != 0 {
				t.Errorf("an unreachable engine charged an attempt: Attempts=%d", e.Attempts)
			}
			return deliveryUnreachable, context.DeadlineExceeded
		}, func(e spoolEntry, err error) {
			t.Error("entry abandoned despite never being rejected by the engine")
		})
	}

	if n := countSpoolFiles(t, s.dir); n != 1 {
		t.Errorf("expected the entry retained across the outage, %d files remain", n)
	}
}

// The attempt count is persisted with the entry, so restarting the agent does
// not hand an undeliverable entry a fresh budget and restart the cycle.
func TestSpool_AttemptCountSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	logger := hclog.NewNullLogger()

	before := newPostbackSpool(dir, 10, time.Hour, 3, logger)
	if err := before.enqueue(
		spoolEntry{PostId: "poison", Result: []byte("x"), CreatedAt: time.Now()},
	); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	before.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
		return deliveryRetryEntry, fmt.Errorf("postback failed: status 503: ")
	}, nil)

	// A fresh spool over the same directory stands in for an agent restart.
	after := newPostbackSpool(dir, 10, time.Hour, 3, logger)
	var seen spoolEntry
	after.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
		seen = e
		return deliveryDone, nil
	}, nil)

	if seen.PostId != "poison" {
		t.Fatalf("expected the entry to survive the restart, got %q", seen.PostId)
	}
	if seen.Attempts != 1 {
		t.Errorf("expected the attempt count to persist across restart, got %d", seen.Attempts)
	}
	if seen.LastError == "" {
		t.Error("expected the last error to persist across restart")
	}
}

// An entry file written by an older agent has no attempts field. It must be
// delivered as a never-attempted entry, not discarded as corrupt — the results
// spooled by the agent being upgraded from are exactly what must not be lost.
func TestSpool_LegacyEntryWithoutAttemptsIsDelivered(t *testing.T) {
	s := newTestSpool(t, 10, time.Hour)
	if err := os.MkdirAll(s.dir, utils.DefaultDirMod); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Exactly the v1.5.0 on-disk shape: post_id, result, created_at, nothing else.
	legacy := fmt.Sprintf(
		`{"post_id":"legacy","result":%q,"created_at":%q}`,
		[]byte("cmVzdWx0"), // base64 of "result", how encoding/json renders []byte
		time.Now().Format(time.RFC3339Nano),
	)
	name := fmt.Sprintf("%020d-%06d%s", time.Now().UnixNano(), 1, spoolFileSuffix)
	if err := os.WriteFile(
		filepath.Join(s.dir, name), []byte(legacy), utils.DefaultFileMod,
	); err != nil {
		t.Fatalf("write legacy entry: %v", err)
	}

	var seen spoolEntry
	delivered := 0
	s.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
		seen = e
		delivered++
		return deliveryDone, nil
	}, nil)

	if delivered != 1 {
		t.Fatalf("expected the legacy entry to be delivered, got %d deliveries", delivered)
	}
	if seen.PostId != "legacy" {
		t.Errorf("expected post_id %q, got %q", "legacy", seen.PostId)
	}
	if seen.Attempts != 0 {
		t.Errorf("expected a legacy entry to read as never attempted, got %d", seen.Attempts)
	}
	if string(seen.Result) != "result" {
		t.Errorf("expected the legacy result to round-trip, got %q", seen.Result)
	}
	if got := s.droppedTotal.Load(); got != 0 {
		t.Errorf("a legacy entry must not be dropped, got %d drops", got)
	}
}

// Entries stranded behind a poisoned entry used to sit untried until maxAge
// discarded them. With the poisoned entry passed over they are delivered on the
// first flush, so nothing ages out at all.
func TestSpool_StrandedEntriesAreDeliveredNotAgedOut(t *testing.T) {
	s := newTestSpool(t, 10, 300*time.Millisecond)

	for _, id := range []string{"poison", "b", "c"} {
		if err := s.enqueue(
			spoolEntry{PostId: id, Result: []byte(id), CreatedAt: time.Now()},
		); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
		time.Sleep(time.Millisecond)
	}

	delivered := map[string]bool{}
	s.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
		if e.PostId == "poison" {
			return deliveryRetryEntry, fmt.Errorf("postback failed: status 503: ")
		}
		delivered[e.PostId] = true
		return deliveryDone, nil
	}, nil)

	for _, id := range []string{"b", "c"} {
		if !delivered[id] {
			t.Errorf("entry %q was stranded behind the poisoned entry", id)
		}
	}
	if got := s.droppedExpired.Load(); got != 0 {
		t.Errorf("no entry should have aged out, got %d expired drops", got)
	}
}

// A corrupt entry is discarded as it always was, and counted under its own
// reason rather than silently.
func TestSpool_CorruptDropIsCounted(t *testing.T) {
	s := newTestSpool(t, 10, time.Hour)
	if err := os.MkdirAll(s.dir, utils.DefaultDirMod); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bad := filepath.Join(s.dir, "00000000000000000001-000001"+spoolFileSuffix)
	if err := os.WriteFile(bad, []byte("not json"), utils.DefaultFileMod); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}

	s.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
		t.Error("a corrupt entry must never reach the deliverer")
		return deliveryDone, nil
	}, nil)

	if got := s.droppedCorrupt.Load(); got != 1 {
		t.Errorf("expected 1 corrupt drop, got %d", got)
	}
	if got := s.droppedExpired.Load(); got != 0 {
		t.Errorf("a corrupt entry must not be counted as expired, got %d", got)
	}
}

// An entry past maxAge is dropped with the expired reason and counted as such,
// so an aged-out result is never confused with one the engine refused.
func TestSpool_ExpiredDropIsCountedAsExpired(t *testing.T) {
	s := newTestSpool(t, 10, 10*time.Millisecond)
	if err := s.enqueue(
		spoolEntry{PostId: "old", Result: []byte("old"), CreatedAt: time.Now()},
	); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	time.Sleep(30 * time.Millisecond)

	s.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
		t.Error("an expired entry must not be delivered")
		return deliveryDone, nil
	}, nil)

	if got := s.droppedExpired.Load(); got != 1 {
		t.Errorf("expected 1 expired drop, got %d", got)
	}
	if got := s.droppedAttempts.Load(); got != 0 {
		t.Errorf("an expired entry must not be counted as attempts-exhausted, got %d", got)
	}
}

// An engine failing wholesale answers 5xx for every entry, which is
// indistinguishable at the HTTP layer from it rejecting each one specifically.
// Attempt counting is therefore spaced in wall-clock time, so a burst of quick
// reconnects cannot spend an entry's whole budget in minutes and abandon a
// result the engine would have accepted on recovery. The entry must still be
// passed over on every one of those flushes — the spacing bounds the counting,
// never the fix.
func TestSpool_RapidRejectionsDoNotBurnTheAttemptBudget(t *testing.T) {
	s := newPostbackSpool(t.TempDir(), 10, time.Hour, 2, hclog.NewNullLogger())
	if s.attemptInterval != minAttemptInterval {
		t.Fatalf("expected the production spacing by default, got %v", s.attemptInterval)
	}

	for _, id := range []string{"poison", "behind"} {
		if err := s.enqueue(
			spoolEntry{PostId: id, Result: []byte(id), CreatedAt: time.Now()},
		); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
		time.Sleep(time.Millisecond)
	}

	// Ten flushes in quick succession, as a flapping connection would produce.
	deliveredBehind := 0
	for range 10 {
		s.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
			if e.PostId == "poison" {
				return deliveryRetryEntry, fmt.Errorf("postback failed: status 503: ")
			}
			deliveredBehind++
			return deliveryDone, nil
		}, func(e spoolEntry, err error) {
			t.Errorf("entry abandoned after only %d spaced attempts", e.Attempts)
		})
	}

	if n := countSpoolFiles(t, s.dir); n != 1 {
		t.Errorf("expected the rejected entry to survive the burst, %d files remain", n)
	}
	if got := s.droppedAttempts.Load(); got != 0 {
		t.Errorf("expected no attempts-exhausted drop within the spacing window, got %d", got)
	}
	// The entry behind it was still delivered on the very first flush, so the
	// spacing did not reintroduce the blocking this change removes.
	if deliveredBehind != 1 {
		t.Errorf("expected the entry behind to be delivered once, got %d", deliveredBehind)
	}
}

// The spacing is not a free pass: once the interval has elapsed the rejection is
// counted again, so an undeliverable entry is still abandoned eventually.
func TestSpool_SpacedRejectionsStillExhaustTheBudget(t *testing.T) {
	s := newPostbackSpool(t.TempDir(), 10, time.Hour, 2, hclog.NewNullLogger())
	s.attemptInterval = 20 * time.Millisecond

	if err := s.enqueue(
		spoolEntry{PostId: "poison", Result: []byte("x"), CreatedAt: time.Now()},
	); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	reject := func(e spoolEntry) (deliveryOutcome, error) {
		return deliveryRetryEntry, fmt.Errorf("postback failed: status 503: ")
	}

	abandoned := 0
	onAbandon := func(e spoolEntry, err error) { abandoned++ }

	s.flush(context.Background(), reject, onAbandon) // counted (first ever)
	s.flush(context.Background(), reject, onAbandon) // too soon, not counted
	if abandoned != 0 {
		t.Fatalf("entry abandoned before the spacing elapsed")
	}

	time.Sleep(40 * time.Millisecond)
	s.flush(context.Background(), reject, onAbandon) // counted, budget spent

	if abandoned != 1 {
		t.Errorf("expected the entry abandoned once the spaced budget ran out, got %d", abandoned)
	}
	if n := countSpoolFiles(t, s.dir); n != 0 {
		t.Errorf("expected the entry removed, %d files remain", n)
	}
}
