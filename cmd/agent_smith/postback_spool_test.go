package main

import (
	"context"
	"encoding/json"
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
	dir := t.TempDir()
	// Evidence established, as for any agent after its first cycle; the
	// clock-evidence tests build their own spool without it.
	primeHeartbeat(t, filepath.Join(dir, spoolHeartbeatFile), time.Now())
	s := newPostbackSpool(dir, maxEntries, maxAge, maxAttempts, hclog.NewNullLogger())
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
		time.Sleep(time.Millisecond) // sleep-ok: distinct CreatedAt/filenames; ordering, not timing
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
		spoolEntry{PostId: "old", Result: []byte("x"), CreatedAt: time.Now().Add(-time.Hour)},
	); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

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
		time.Sleep(time.Millisecond) // sleep-ok: distinct CreatedAt/filenames; ordering, not timing
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
// first flush, so nothing ages out at all. maxAge is an hour: the assertion is
// that nothing ages out, and a 300ms maxAge against the real clock aged all
// three entries out on a Windows runner under load (sc-119840's soak).
func TestSpool_StrandedEntriesAreDeliveredNotAgedOut(t *testing.T) {
	s := newTestSpool(t, 10, time.Hour)

	for _, id := range []string{"poison", "b", "c"} {
		if err := s.enqueue(
			spoolEntry{PostId: id, Result: []byte(id), CreatedAt: time.Now()},
		); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
		time.Sleep(time.Millisecond) // sleep-ok: distinct CreatedAt/filenames; ordering, not timing
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
		spoolEntry{PostId: "old", Result: []byte("old"), CreatedAt: time.Now().Add(-time.Hour)},
	); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

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
		time.Sleep(time.Millisecond) // sleep-ok: distinct CreatedAt/filenames; ordering, not timing
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
	clock := time.Now()
	s.now = func() time.Time { return clock }

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

	clock = clock.Add(
		40 * time.Millisecond,
	) // the spacing interval elapses on the spool's clock
	s.flush(context.Background(), reject, onAbandon) // counted, budget spent

	if abandoned != 1 {
		t.Errorf("expected the entry abandoned once the spaced budget ran out, got %d", abandoned)
	}
	if n := countSpoolFiles(t, s.dir); n != 0 {
		t.Errorf("expected the entry removed, %d files remain", n)
	}
}

// throttleError is the error a throttled delivery returns in these tests: it
// carries the wait the way engineThrottled does for the flush.
type throttleError struct{ after time.Duration }

func (e *throttleError) Error() string { return "postback throttled: status 429" }

func (e *throttleError) RetryAfter() time.Duration { return e.after }

// A throttle (429, or 503 with Retry-After) is not this entry's fault: it is
// not counted against the attempt budget, the flush stops at it and records
// the time the engine allowed the next attempt - on the entry, in the health
// object and as the value it returns - and a flush inside that window
// attempts nothing (sc-132997).
func TestSpool_ThrottleIsNotCountedAndPausesTheFlush(t *testing.T) {
	s := newTestSpoolWithAttempts(t, 10, time.Hour, 2)
	clock := time.Now()
	s.now = func() time.Time { return clock }

	for _, id := range []string{"first", "behind"} {
		if err := s.enqueue(
			spoolEntry{PostId: id, Result: []byte(id), CreatedAt: clock},
		); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
		time.Sleep(time.Millisecond) // sleep-ok: distinct CreatedAt/filenames; ordering, not timing
	}

	attempted := []string{}
	throttle := true
	deliver := func(e spoolEntry) (deliveryOutcome, error) {
		attempted = append(attempted, e.PostId)
		if throttle {
			return deliveryThrottled, &throttleError{after: 30 * time.Second}
		}
		return deliveryDone, nil
	}
	onAbandon := func(e spoolEntry, err error) {
		t.Errorf("entry %s abandoned on a throttle", e.PostId)
	}

	retryAt := s.flush(context.Background(), deliver, onAbandon)
	if want := clock.Add(30 * time.Second); !retryAt.Equal(want) {
		t.Errorf("flush returned %s, want the engine's time %s", retryAt, want)
	}
	if len(attempted) != 1 || attempted[0] != "first" {
		t.Errorf(
			"attempted %v, want only the first entry: the engine asked the whole agent to back off",
			attempted,
		)
	}
	if n := countSpoolFiles(t, s.dir); n != 2 {
		t.Errorf("expected both entries kept, %d remain", n)
	}

	// Persisted on the entry, not counted against it.
	entries := readSpoolEntries(t, s.dir)
	if entries[0].Attempts != 0 || !entries[0].LastAttemptAt.IsZero() {
		t.Errorf("throttle was counted as a rejection: %+v", entries[0])
	}
	if !entries[0].NextAttemptAt.Equal(clock.Add(30 * time.Second)) {
		t.Errorf(
			"next_attempt_at = %s, want %s",
			entries[0].NextAttemptAt,
			clock.Add(30*time.Second),
		)
	}
	if h := s.health(); h.NextAttemptAt != clock.Add(30*time.Second).UTC().Format(time.RFC3339) {
		t.Errorf("health next_attempt_at = %q", h.NextAttemptAt)
	}

	// Inside the window a flush attempts nothing and returns the same time.
	clock = clock.Add(10 * time.Second)
	if again := s.flush(context.Background(), deliver, onAbandon); !again.Equal(retryAt) {
		t.Errorf("flush inside the window returned %s, want %s", again, retryAt)
	}
	if len(attempted) != 1 {
		t.Errorf("a flush inside the window attempted %v", attempted[1:])
	}

	// Once it has passed, everything is delivered and the pause is gone.
	clock = clock.Add(30 * time.Second)
	throttle = false
	if done := s.flush(context.Background(), deliver, onAbandon); !done.IsZero() {
		t.Errorf("flush after the window returned %s, want zero", done)
	}
	if len(attempted) != 3 {
		t.Errorf("attempted %v, want both entries after the window", attempted)
	}
	if n := countSpoolFiles(t, s.dir); n != 0 {
		t.Errorf("expected the spool emptied, %d remain", n)
	}
	if h := s.health(); h.NextAttemptAt != "" {
		t.Errorf("health still reports next_attempt_at %q after the window", h.NextAttemptAt)
	}
	if got := s.droppedTotal.Load(); got != 0 {
		t.Errorf("a throttle must drop nothing, got %d drops", got)
	}
}

// The recorded time survives a restart: a new process reads it off the entry
// and does not attempt it (or anything behind it) before the engine's time.
func TestSpool_ThrottleSurvivesRestart(t *testing.T) {
	s := newTestSpool(t, 10, time.Hour)
	clock := time.Now()
	s.now = func() time.Time { return clock }
	if err := s.enqueue(
		spoolEntry{PostId: "first", Result: []byte("x"), CreatedAt: clock},
	); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	s.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
		return deliveryThrottled, &throttleError{after: time.Minute}
	}, nil)

	// A new spool over the same directory: the in-memory pause is gone.
	restarted := newPostbackSpool(
		s.dir,
		10,
		time.Hour,
		defaultSpoolMaxAttempts,
		hclog.NewNullLogger(),
	)
	restarted.now = func() time.Time { return clock.Add(20 * time.Second) }
	attempted := 0
	retryAt := restarted.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
		attempted++
		return deliveryDone, nil
	}, nil)
	if attempted != 0 {
		t.Errorf(
			"the restarted agent attempted the entry %d time(s) inside the engine's window",
			attempted,
		)
	}
	if want := clock.Add(time.Minute); !retryAt.Equal(want) {
		t.Errorf("flush returned %s, want the recorded %s", retryAt, want)
	}
	if h := restarted.health(); h.NextAttemptAt == "" {
		t.Error("the recovered pause is not reported in the health object")
	}

	// After the window the entry is delivered and the cleared field persisted
	// is moot: the file is gone.
	restarted.now = func() time.Time { return clock.Add(2 * time.Minute) }
	if again := restarted.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
		attempted++
		if !e.NextAttemptAt.IsZero() {
			t.Errorf("entry handed to deliver still carries next_attempt_at %s", e.NextAttemptAt)
		}
		return deliveryDone, nil
	}, nil); !again.IsZero() || attempted != 1 {
		t.Errorf("after the window: flush returned %s, attempted %d", again, attempted)
	}
}

// A rejection after a throttle clears the recorded time on the persisted
// entry, so a stale next_attempt_at never outlives the attempt it bounded.
func TestSpool_RejectionAfterThrottleClearsNextAttempt(t *testing.T) {
	s := newTestSpool(t, 10, time.Hour)
	clock := time.Now()
	s.now = func() time.Time { return clock }
	if err := s.enqueue(
		spoolEntry{PostId: "first", Result: []byte("x"), CreatedAt: clock},
	); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	s.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
		return deliveryThrottled, &throttleError{after: time.Second}
	}, nil)
	clock = clock.Add(2 * time.Second)
	s.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
		return deliveryRetryEntry, fmt.Errorf("postback failed: status 503: ")
	}, nil)

	entries := readSpoolEntries(t, s.dir)
	if len(entries) != 1 {
		t.Fatalf("expected the rejected entry kept, got %d", len(entries))
	}
	if !entries[0].NextAttemptAt.IsZero() {
		t.Errorf(
			"next_attempt_at %s persisted past the attempt it bounded",
			entries[0].NextAttemptAt,
		)
	}
	if entries[0].Attempts != 1 {
		t.Errorf(
			"the rejection after the throttle was not counted: attempts = %d",
			entries[0].Attempts,
		)
	}
}

// readSpoolEntries decodes every entry file oldest-first.
func readSpoolEntries(t *testing.T, dir string) []spoolEntry {
	t.Helper()
	names, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read spool dir: %v", err)
	}
	var entries []spoolEntry
	for _, de := range names {
		if filepath.Ext(de.Name()) != spoolFileSuffix {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, de.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", de.Name(), err)
		}
		var e spoolEntry
		if err := json.Unmarshal(raw, &e); err != nil {
			t.Fatalf("decode %s: %v", de.Name(), err)
		}
		entries = append(entries, e)
	}
	return entries
}
