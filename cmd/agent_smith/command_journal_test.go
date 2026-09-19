package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestJournal(t *testing.T) (*commandJournal, func(time.Duration)) {
	t.Helper()
	j := newCommandJournal(filepath.Join(t.TempDir(), "command_journal"), 5, time.Hour)
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	j.now = func() time.Time { return now }
	return j, func(d time.Duration) { now = now.Add(d) }
}

func TestJournalKey_UsesPostIdWhenPresentElseDigest(t *testing.T) {
	withID := postbackPayload("echo hi", "abc:def")
	if k := journalKey(withID); k != "post:abc:def" {
		t.Errorf("key for a payload with post_id = %q, want post:abc:def", k)
	}
	noID := validPayload("echo hi")
	k := journalKey(noID)
	if !strings.HasPrefix(k, "sha256:") {
		t.Errorf("key for a payload without post_id = %q, want a sha256: digest", k)
	}
	if journalKey(noID) != k {
		t.Error("digest key is not stable across calls")
	}
	if journalKey([]byte("not json at all")) == "" {
		t.Error("an unparseable payload must still get a key so it is journaled")
	}
}

func TestJournal_FilenamesAreWindowsSafeAndKeyDerivable(t *testing.T) {
	j, _ := newTestJournal(t)
	key := "post:019a__r0:01a0-b395" // post_id carries colons
	if _, err := j.put(key, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(j.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected one file, got %d", len(entries))
	}
	name := entries[0].Name()
	if strings.ContainsAny(name, `:*?"<>|`) {
		t.Errorf("journal filename %q contains a character Windows rejects", name)
	}
	if filepath.Base(j.pendingPath(key)) != name {
		t.Errorf(
			"pendingPath(key) = %q does not resolve to the file written, %q",
			j.pendingPath(key),
			name,
		)
	}
}

func TestJournal_PutReportsRedeliveryOfPendingAndCompleted(t *testing.T) {
	j, _ := newTestJournal(t)
	existed, err := j.put("k1", []byte("a"))
	if err != nil || existed {
		t.Fatalf("first put: existed=%v err=%v", existed, err)
	}
	existed, err = j.put("k1", []byte("a"))
	if err != nil || !existed {
		t.Errorf("redelivery while pending: existed=%v err=%v, want true", existed, err)
	}
	if err := j.complete("k1"); err != nil {
		t.Fatal(err)
	}
	existed, err = j.put("k1", []byte("a"))
	if err != nil || !existed {
		t.Errorf(
			"redelivery after completion: existed=%v err=%v, want true (tombstone)",
			existed,
			err,
		)
	}
	fresh, expired, err := j.pending()
	if err != nil || len(fresh) != 0 || len(expired) != 0 {
		t.Errorf(
			"a completed entry must not be pending: fresh=%d expired=%d err=%v",
			len(fresh),
			len(expired),
			err,
		)
	}
}

func TestJournal_TombstoneExpiresSoAStaleRedeliveryIsNotBlockedForever(t *testing.T) {
	j, advance := newTestJournal(t)
	if _, err := j.put("k1", []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := j.complete("k1"); err != nil {
		t.Fatal(err)
	}
	advance(time.Hour + time.Second) // past the broker's own TTL
	existed, err := j.put("k1", []byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if existed {
		t.Error("tombstone older than max age still de-duplicated; it should have been pruned")
	}
}

func TestJournal_PendingIsOldestFirstAndSplitsExpired(t *testing.T) {
	j, advance := newTestJournal(t)
	if _, err := j.put("old", []byte("1")); err != nil {
		t.Fatal(err)
	}
	advance(30 * time.Minute)
	if _, err := j.put("mid", []byte("2")); err != nil {
		t.Fatal(err)
	}
	advance(45 * time.Minute) // "old" is now 75 min: past the 1h max age
	if _, err := j.put("new", []byte("3")); err != nil {
		t.Fatal(err)
	}

	fresh, expired, err := j.pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(expired) != 1 || expired[0].Key != "old" {
		t.Errorf("expired = %v, want just 'old'", keysOf(expired))
	}
	if got := keysOf(fresh); len(got) != 2 || got[0] != "mid" || got[1] != "new" {
		t.Errorf("fresh order = %v, want [mid new] (oldest first)", got)
	}
}

func TestJournal_MarkStartedSurvivesAndIsVisibleOnReplay(t *testing.T) {
	j, _ := newTestJournal(t)
	if _, err := j.put("k1", []byte("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := j.put("k2", []byte("b")); err != nil {
		t.Fatal(err)
	}
	if err := j.markStarted("k1"); err != nil {
		t.Fatal(err)
	}
	fresh, _, err := j.pending()
	if err != nil {
		t.Fatal(err)
	}
	var started, unstarted int
	for _, e := range fresh {
		if e.StartedAt.IsZero() {
			unstarted++
		} else {
			started++
		}
	}
	if started != 1 || unstarted != 1 {
		t.Errorf("started=%d unstarted=%d, want 1 and 1", started, unstarted)
	}
}

func TestJournal_FullReturnsErrJournalFullWithoutWriting(t *testing.T) {
	j, _ := newTestJournal(t) // maxPending 5
	for i := 0; i < 5; i++ {
		if _, err := j.put(string(rune('a'+i)), []byte("x")); err != nil {
			t.Fatal(err)
		}
	}
	_, err := j.put("overflow", []byte("x"))
	if err != errJournalFull {
		t.Errorf("put at the bound returned %v, want errJournalFull", err)
	}
	if fileExists(j.pendingPath("overflow")) {
		t.Error("an entry was written despite the journal being full")
	}
}

func TestJournal_DiscardRemovesWithoutTombstone(t *testing.T) {
	j, _ := newTestJournal(t)
	if _, err := j.put("k1", []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := j.discard("k1"); err != nil {
		t.Fatal(err)
	}
	if fileExists(j.pendingPath("k1")) || fileExists(j.donePath("k1")) {
		t.Error("discard left a pending entry or a tombstone behind")
	}
}

func TestJournal_UnreadableEntryIsRemovedNotReplayedForever(t *testing.T) {
	j, _ := newTestJournal(t)
	if err := os.MkdirAll(j.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(j.dir, "deadbeef"+journalPendingSuffix)
	if err := os.WriteFile(bad, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	fresh, expired, err := j.pending()
	if err != nil || len(fresh) != 0 || len(expired) != 0 {
		t.Errorf(
			"unreadable entry surfaced: fresh=%d expired=%d err=%v",
			len(fresh),
			len(expired),
			err,
		)
	}
	if fileExists(bad) {
		t.Error("unreadable entry was left in place; it would be re-reported on every start")
	}
}

func TestJournal_WriteIsAtomic_NoTempFileLeftBehind(t *testing.T) {
	j, _ := newTestJournal(t)
	if _, err := j.put("k1", []byte("a")); err != nil {
		t.Fatal(err)
	}
	if err := j.markStarted("k1"); err != nil {
		t.Fatal(err)
	}
	if err := j.complete("k1"); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(j.dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("temp file %q left behind", e.Name())
		}
	}
}

func keysOf(es []journalEntry) []string {
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.Key)
	}
	return out
}
