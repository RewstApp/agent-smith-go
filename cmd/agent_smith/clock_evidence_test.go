package main

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
)

// testClock is an injected wall clock shared by successive journal or spool
// instances on the same directory, so a test can play a restart.
type testClock struct{ now time.Time }

func (c *testClock) read() time.Time         { return c.now }
func (c *testClock) advance(d time.Duration) { c.now = c.now.Add(d) }
func (c *testClock) set(t time.Time)         { c.now = t }

var t0 = time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)

// openJournal is a fresh process's view of dir: a new journal instance on the
// shared clock, with no in-memory state carried over.
func openJournal(dir string, clock *testClock, stepped *atomic.Int32) *commandJournal {
	j := newCommandJournal(dir, 5, time.Hour)
	j.now = clock.read
	j.onClockStepBack = func(time.Time, time.Time) { stepped.Add(1) }
	return j
}

func heartbeatOf(t *testing.T, path string) time.Time {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	ts, err := time.Parse(time.RFC3339Nano, string(data))
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// The ticket's first case: a command queued seconds before a reboot, and a
// clock that steps forward by a day when NTP syncs after it. The journal has
// no evidence those hours passed, so the command is replayed, not expired.
func TestJournalClock_ForwardStepAtBootDoesNotExpire(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "j")
	clock := &testClock{now: t0}
	var stepped atomic.Int32

	before := openJournal(dir, clock, &stepped)
	if _, err := before.put("k", []byte("1")); err != nil {
		t.Fatal(err)
	}
	clock.advance(5 * time.Minute)
	before.observeClock() // the heartbeat the previous process left: t0+5m

	clock.advance(25 * time.Hour) // the step at boot
	after := openJournal(dir, clock, &stepped)
	fresh, expired, err := after.pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 0 || len(fresh) != 1 || fresh[0].Key != "k" {
		t.Fatalf("fresh=%v expired=%v; want the command replayed", keysOf(fresh), keysOf(expired))
	}
	if stepped.Load() != 0 {
		t.Error("a forward step was reported as a backward one")
	}
}

// Ageing the previous process actually saw still expires: the heartbeat moved
// two hours past the receipt before it died.
func TestJournalClock_ObservedAgeingAcrossRestartExpires(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "j")
	clock := &testClock{now: t0}
	var stepped atomic.Int32

	before := openJournal(dir, clock, &stepped)
	if _, err := before.put("old", []byte("1")); err != nil {
		t.Fatal(err)
	}
	clock.advance(2 * time.Hour)
	before.observeClock()
	if _, err := before.put("young", []byte("2")); err != nil {
		t.Fatal(err)
	}

	clock.advance(time.Minute)
	after := openJournal(dir, clock, &stepped)
	fresh, expired, err := after.pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || expired[0].Key != "old" {
		t.Errorf("expired = %v, want [old]", keysOf(expired))
	}
	if len(fresh) != 1 || fresh[0].Key != "young" {
		t.Errorf("fresh = %v, want [young]", keysOf(fresh))
	}
}

// The ticket's second case: a clock stepped back three days. Nothing is
// expired, a completed command's tombstone still de-duplicates its redelivery,
// the step is reported exactly once, and time is measured from the new clock
// from then on.
func TestJournalClock_BackwardStepSuspendsExpiryAndKeepsTombstones(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "j")
	clock := &testClock{now: t0}
	var stepped atomic.Int32

	before := openJournal(dir, clock, &stepped)
	if _, err := before.put("done", []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := before.complete("done"); err != nil {
		t.Fatal(err)
	}
	if _, err := before.put("queued", []byte("2")); err != nil {
		t.Fatal(err)
	}

	clock.set(t0.Add(-72 * time.Hour))
	after := openJournal(dir, clock, &stepped)
	fresh, expired, err := after.pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 0 || len(fresh) != 1 {
		t.Fatalf(
			"fresh=%v expired=%v; nothing may expire after a backward step",
			keysOf(fresh),
			keysOf(expired),
		)
	}
	if stepped.Load() != 1 {
		t.Fatalf("backward step reported %d times, want once", stepped.Load())
	}
	// The redelivery of the completed command is still recognised.
	existed, err := after.put("done", []byte("1"))
	if err != nil {
		t.Fatal(err)
	}
	if !existed {
		t.Error("tombstone pruned after a backward step; the command would run twice")
	}
	// Evidence restarted in the new timeline: the heartbeat is the new clock.
	if hb := heartbeatOf(t, filepath.Join(dir, journalHeartbeatFile)); !hb.Equal(clock.read()) {
		t.Errorf("heartbeat = %s, want the new clock %s", hb, clock.read())
	}
	// A later cycle in the new timeline ages normally and does not re-report.
	if _, err := after.put("new", []byte("3")); err != nil {
		t.Fatal(err)
	}
	clock.advance(2 * time.Hour)
	after.observeClock() // the process saw those two hours pass before it died
	later := openJournal(dir, clock, &stepped)
	fresh, expired, err = later.pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || expired[0].Key != "new" {
		t.Errorf("expired = %v, want [new]: the new timeline's own ageing counts", keysOf(expired))
	}
	if len(fresh) != 1 || fresh[0].Key != "queued" {
		t.Errorf(
			"fresh = %v, want [queued]: a record from the old timeline never ages until the clock catches up",
			keysOf(fresh),
		)
	}
	if stepped.Load() != 1 {
		t.Errorf("backward step reported %d times in total, want once", stepped.Load())
	}
}

// No heartbeat at all - the first run after installing this version - expires
// nothing in that cycle and establishes the evidence for the next.
func TestJournalClock_AbsentHeartbeatExpiresNothingThenEstablishesEvidence(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "j")
	clock := &testClock{now: t0}
	var stepped atomic.Int32

	before := openJournal(dir, clock, &stepped)
	if _, err := before.put("k", []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, journalHeartbeatFile)); err != nil {
		t.Fatal(err)
	}

	clock.advance(25 * time.Hour)
	first := openJournal(dir, clock, &stepped)
	fresh, expired, err := first.pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 0 || len(fresh) != 1 {
		t.Fatalf(
			"first cycle without evidence: fresh=%v expired=%v",
			keysOf(fresh),
			keysOf(expired),
		)
	}
	if _, err := os.Stat(filepath.Join(dir, journalHeartbeatFile)); err != nil {
		t.Fatalf("heartbeat not established: %v", err)
	}
	// The next process has evidence: the baseline it loads is 25h past the
	// entry, which the previous (this) process did observe.
	clock.advance(time.Minute)
	second := openJournal(dir, clock, &stepped)
	_, expired, err = second.pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 {
		t.Errorf("expired = %v, want the entry once evidence exists", keysOf(expired))
	}
	if stepped.Load() != 0 {
		t.Error("an absent heartbeat was reported as a backward step")
	}
}

func TestJournalClock_HeartbeatIsWrittenAtMostOncePerInterval(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "j")
	clock := &testClock{now: t0}
	var stepped atomic.Int32
	j := openJournal(dir, clock, &stepped)
	path := filepath.Join(dir, journalHeartbeatFile)

	if _, err := j.put("k", []byte("1")); err != nil {
		t.Fatal(err)
	}
	if hb := heartbeatOf(t, path); !hb.Equal(t0) {
		t.Fatalf("heartbeat after first write = %s, want %s", hb, t0)
	}
	clock.advance(30 * time.Second)
	j.observeClock()
	if hb := heartbeatOf(t, path); !hb.Equal(t0) {
		t.Errorf("heartbeat rewritten after 30s: %s", hb)
	}
	clock.advance(31 * time.Second)
	j.observeClock()
	if hb := heartbeatOf(t, path); !hb.Equal(t0.Add(61 * time.Second)) {
		t.Errorf("heartbeat not advanced after 61s: %s", hb)
	}
}

// The spool follows the same rule: a result spooled just before a reboot
// survives a forward step at boot and is delivered.
func TestSpoolClock_ForwardStepAtBootDoesNotExpire(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	clock := &testClock{now: t0}
	open := func() *postbackSpool {
		s := newPostbackSpool(dir, 10, time.Hour, defaultSpoolMaxAttempts, hclog.NewNullLogger())
		s.now = clock.read
		s.attemptInterval = 0
		return s
	}

	before := open()
	entry := spoolEntry{PostId: "p", Result: []byte("x"), CreatedAt: clock.read()}
	if err := before.enqueue(entry); err != nil {
		t.Fatal(err)
	}
	clock.advance(25 * time.Hour)
	after := open()
	var delivered []string
	after.flush(context.Background(), func(e spoolEntry) (deliveryOutcome, error) {
		delivered = append(delivered, e.PostId)
		return deliveryDone, nil
	}, nil)
	if len(delivered) != 1 || delivered[0] != "p" {
		t.Fatalf("delivered %v; the result must not be expired on unobserved time", delivered)
	}
	if n := after.droppedExpired.Load(); n != 0 {
		t.Errorf("expired drops = %d, want 0", n)
	}
}

func TestSpoolClock_ObservedAgeingAcrossRestartExpires(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spool")
	clock := &testClock{now: t0}
	open := func() *postbackSpool {
		s := newPostbackSpool(dir, 10, time.Hour, defaultSpoolMaxAttempts, hclog.NewNullLogger())
		s.now = clock.read
		s.attemptInterval = 0
		return s
	}
	before := open()
	entry := spoolEntry{PostId: "p", Result: []byte("x"), CreatedAt: clock.read()}
	if err := before.enqueue(entry); err != nil {
		t.Fatal(err)
	}
	clock.advance(2 * time.Hour)
	before.clock.observe()

	clock.advance(time.Minute)
	after := open()
	attempts := 0
	after.flush(context.Background(), func(spoolEntry) (deliveryOutcome, error) {
		attempts++
		return deliveryDone, nil
	}, nil)
	if attempts != 0 || after.droppedExpired.Load() != 1 {
		t.Fatalf(
			"attempts=%d expired=%d; the previous process saw it age past the bound",
			attempts,
			after.droppedExpired.Load(),
		)
	}
}
