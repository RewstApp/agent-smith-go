package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
)

func newTestSpool(t *testing.T, maxEntries int, maxAge time.Duration) *postbackSpool {
	t.Helper()
	return newPostbackSpool(t.TempDir(), maxEntries, maxAge, hclog.NewNullLogger())
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
	s.flush(context.Background(), func(e spoolEntry) (bool, error) {
		delivered = append(delivered, e.PostId)
		return true, nil
	})

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

// TestSpool_FlushStopsOnTransientFailure verifies that a transient (done=false)
// result halts the flush and leaves every remaining entry on disk for a later
// cycle, preserving order.
func TestSpool_FlushStopsOnTransientFailure(t *testing.T) {
	s := newTestSpool(t, 10, time.Hour)

	for _, id := range []string{"a", "b", "c"} {
		if err := s.enqueue(
			spoolEntry{PostId: id, Result: []byte(id), CreatedAt: time.Now()},
		); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
	}

	var attempts int
	s.flush(context.Background(), func(e spoolEntry) (bool, error) {
		attempts++
		return false, context.DeadlineExceeded // transient
	})

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
	s.flush(context.Background(), func(e spoolEntry) (bool, error) {
		delivered = append(delivered, e.PostId)
		return true, nil
	})
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
	s.flush(context.Background(), func(e spoolEntry) (bool, error) {
		attempts++
		return true, nil
	})

	if attempts != 0 {
		t.Errorf("expected expired entry not to be delivered, got %d attempts", attempts)
	}
	if n := countSpoolFiles(t, s.dir); n != 0 {
		t.Errorf("expected expired entry removed, %d remain", n)
	}
}

// TestSpool_AgeBoundOnEnqueue verifies that a later enqueue prunes entries that
// have aged out, bounding growth even without a flush.
func TestSpool_AgeBoundOnEnqueue(t *testing.T) {
	s := newTestSpool(t, 10, 10*time.Millisecond)

	if err := s.enqueue(
		spoolEntry{PostId: "old", Result: []byte("x"), CreatedAt: time.Now()},
	); err != nil {
		t.Fatalf("enqueue old: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if err := s.enqueue(
		spoolEntry{PostId: "new", Result: []byte("y"), CreatedAt: time.Now()},
	); err != nil {
		t.Fatalf("enqueue new: %v", err)
	}

	if n := countSpoolFiles(t, s.dir); n != 1 {
		t.Fatalf("expected expired entry pruned on enqueue, %d files remain", n)
	}

	var delivered []string
	s.flush(context.Background(), func(e spoolEntry) (bool, error) {
		delivered = append(delivered, e.PostId)
		return true, nil
	})
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
	s.flush(context.Background(), func(e spoolEntry) (bool, error) {
		attempts++
		return true, nil
	})

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
	s.flush(ctx, func(e spoolEntry) (bool, error) {
		attempts++
		return true, nil
	})

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
	s.flush(context.Background(), func(e spoolEntry) (bool, error) {
		attempts++
		return true, nil
	})
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
	s.flush(context.Background(), func(e spoolEntry) (bool, error) {
		got = e.Result
		return true, nil
	})
	if string(got) != string(payload) {
		t.Errorf("result payload corrupted: got %q want %q", got, payload)
	}
}
