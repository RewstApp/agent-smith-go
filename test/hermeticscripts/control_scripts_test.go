package hermeticscripts

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The sc-126322 helper scripts drive and read the fixture's control surface
// (scripted postback responses, the lost-PUBACK arm, the device snapshot) or
// write files the agent reads (the seeded journal). This stub records what
// they send and serves what they poll, so a drift between a script and the
// surface fails here rather than ten minutes into a CI run.
type edgeStub struct {
	mu        sync.Mutex
	scripts   []map[string]any
	loseCalls []string
	responses map[string]any
	snapshot  map[string]any
	srv       *httptest.Server
}

func newEdgeStub(t *testing.T) *edgeStub {
	t.Helper()
	s := &edgeStub{responses: map[string]any{}, snapshot: map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/_control/postback-responses", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			var req map[string]any
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			s.mu.Lock()
			s.scripts = append(s.scripts, req)
			s.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"post_id": req["post_id"], "attempts": 0})
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(s.responses)
	})
	mux.HandleFunc("/_control/devices/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/lose-next-puback") {
			s.mu.Lock()
			s.loseCalls = append(s.loseCalls, r.URL.Path)
			s.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"lose_next_puback": true})
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(s.snapshot)
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

// serve publishes what the GET handlers answer; the handlers run on the
// server's goroutines, so the hand-off goes through the mutex (the test
// workflow runs this package under -race).
func (s *edgeStub) serve(responses, snapshot map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if responses != nil {
		s.responses = responses
	}
	if snapshot != nil {
		s.snapshot = snapshot
	}
}

func TestScriptPostbackResponses_PostsTheStatusesInOrder(t *testing.T) {
	s := newEdgeStub(t)
	code, out := run(t, "hermetic-script-postback-responses.ps1",
		"ENGINE_URL="+s.srv.URL, "POST_ID=spool-1", "STATUSES=503, 503,200")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	s.mu.Lock()
	scripts := append([]map[string]any(nil), s.scripts...)
	s.mu.Unlock()
	if len(scripts) != 1 {
		t.Fatalf("scripts posted: %d", len(scripts))
	}
	if got := fmt.Sprint(scripts[0]["statuses"]); got != "[503 503 200]" {
		t.Errorf("statuses %s", got)
	}
	if scripts[0]["post_id"] != "spool-1" {
		t.Errorf("post_id %v", scripts[0]["post_id"])
	}
}

func TestScriptPostbackResponses_RequiresAPostIDAndStatuses(t *testing.T) {
	s := newEdgeStub(t)
	code, _ := run(t, "hermetic-script-postback-responses.ps1",
		"ENGINE_URL="+s.srv.URL, "POST_ID=x", "STATUSES=")
	if code == 0 {
		t.Error("an empty STATUSES passed")
	}
}

func TestAssertPostbackResponses_PassesWhenRecordedAndAttemptsMatch(t *testing.T) {
	s := newEdgeStub(t)
	s.serve(map[string]any{
		"post_id":   "spool-1",
		"attempts":  4,
		"recorded":  true,
		"remaining": []int{},
	}, nil)
	code, out := run(
		t,
		"hermetic-assert-postback-responses.ps1",
		"ENGINE_URL="+s.srv.URL,
		"POST_ID=spool-1",
		"RECORDED=true",
		"MIN_ATTEMPTS=3",
		"MAX_ATTEMPTS=4",
		"TIMEOUT_SECONDS=5",
	)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
}

func TestAssertPostbackResponses_FailsOnAnUnexpectedRecord(t *testing.T) {
	s := newEdgeStub(t)
	s.serve(map[string]any{"post_id": "reject-1", "attempts": 7, "recorded": true}, nil)
	code, out := run(
		t,
		"hermetic-assert-postback-responses.ps1",
		"ENGINE_URL="+s.srv.URL,
		"POST_ID=reject-1",
		"RECORDED=false",
		"MIN_ATTEMPTS=7",
		"TIMEOUT_SECONDS=3",
	)
	if code == 0 {
		t.Fatal("a recorded result passed a RECORDED=false assertion")
	}
	if !strings.Contains(out, "recorded is True, expected False") {
		t.Errorf("output does not name the mismatch:\n%s", out)
	}
	s.serve(map[string]any{"post_id": "dup-1", "attempts": 2, "recorded": true}, nil)
	code, out = run(
		t,
		"hermetic-assert-postback-responses.ps1",
		"ENGINE_URL="+s.srv.URL,
		"POST_ID=dup-1",
		"RECORDED=true",
		"MAX_ATTEMPTS=1",
		"TIMEOUT_SECONDS=3",
	)
	if code == 0 || !strings.Contains(out, "at most 1") {
		t.Errorf("a second postback passed MAX_ATTEMPTS=1 (exit %d):\n%s", code, out)
	}
}

func TestLoseNextPuback_PostsTheDeviceControl(t *testing.T) {
	s := newEdgeStub(t)
	code, out := run(t, "hermetic-lose-next-puback.ps1", "ENGINE_URL="+s.srv.URL, "DEVICE_ID=dev-9")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	s.mu.Lock()
	calls := append([]string(nil), s.loseCalls...)
	s.mu.Unlock()
	if len(calls) != 1 || calls[0] != "/_control/devices/dev-9/lose-next-puback" {
		t.Errorf("control calls %v", calls)
	}
}

func TestAssertDeviceSnapshot_ChecksEveryGivenField(t *testing.T) {
	s := newEdgeStub(t)
	s.serve(nil, map[string]any{
		"queued":    0,
		"inflight":  1,
		"delivered": 0,
		"pubacks":   1,
		"connects":  1,
	})
	code, out := run(
		t,
		"hermetic-assert-device-snapshot.ps1",
		"ENGINE_URL="+s.srv.URL,
		"DEVICE_ID=dev-9",
		"INFLIGHT=1",
		"DELIVERED=0",
		"MIN_PUBACKS=1",
		"TIMEOUT_SECONDS=5",
	)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	code, out = run(t, "hermetic-assert-device-snapshot.ps1",
		"ENGINE_URL="+s.srv.URL, "DEVICE_ID=dev-9", "DELIVERED=1", "TIMEOUT_SECONDS=3")
	if code == 0 || !strings.Contains(out, "delivered=0, expected 1") {
		t.Errorf("a wrong delivered count passed (exit %d):\n%s", code, out)
	}
}

func TestAssertLogCountExactly_FailsOnMoreAsWellAsFewer(t *testing.T) {
	log := filepath.Join(t.TempDir(), "agent.log")
	write := func(lines ...string) {
		t.Helper()
		if err := os.WriteFile(log, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("old: Command journal unavailable", "x", "new: Command journal unavailable")
	code, out := run(
		t,
		"hermetic-assert-log-count-exactly.ps1",
		"LOG_FILE="+log,
		"PATTERN=Command journal unavailable",
		"EXPECTED=1",
		"BASELINE=1",
		"TIMEOUT_SECONDS=3",
	)
	if code != 0 {
		t.Fatalf("exactly one past the baseline failed: exit %d\n%s", code, out)
	}
	write(
		"a: Command journal unavailable",
		"b: Command journal unavailable",
		"c: Command journal unavailable",
	)
	code, out = run(
		t,
		"hermetic-assert-log-count-exactly.ps1",
		"LOG_FILE="+log,
		"PATTERN=Command journal unavailable",
		"EXPECTED=1",
		"BASELINE=1",
		"TIMEOUT_SECONDS=3",
	)
	if code == 0 || !strings.Contains(out, "appeared 2 time(s)") {
		t.Errorf("two past the baseline passed EXPECTED=1 (exit %d):\n%s", code, out)
	}
	write("nothing here")
	code, _ = run(t, "hermetic-assert-log-count-exactly.ps1",
		"LOG_FILE="+log, "PATTERN=Command journal unavailable",
		"EXPECTED=1", "BASELINE=0", "TIMEOUT_SECONDS=2")
	if code == 0 {
		t.Error("a missing line passed EXPECTED=1")
	}
}

// The seeded journal entry must be exactly what cmd/agent_smith/command_journal.go
// reads: <hex(sha256("post:<id>"))[:32]>.json holding {key, payload, received_at}
// with the payload the C2D message (commands UTF-16LE then base64), a
// heartbeat in RFC3339Nano UTC, and the clock offset override when asked for.
func TestSeedJournal_WritesWhatTheAgentReads(t *testing.T) {
	data := t.TempDir()
	code, out := run(
		t,
		"hermetic-seed-journal.ps1",
		"DATA_DIR="+data,
		"POST_ID=clock-1",
		"COMMANDS=echo ok",
		"RECEIVED_AGO_SECONDS=120",
		"CLOCK_OFFSET=25h",
	)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	sum := sha256.Sum256([]byte("post:clock-1"))
	entryPath := filepath.Join(data, "command_journal", hex.EncodeToString(sum[:16])+".json")
	raw, err := os.ReadFile(entryPath)
	if err != nil {
		t.Fatalf("entry not where the agent looks: %v\n%s", err, out)
	}
	var entry struct {
		Key        string    `json:"key"`
		Payload    []byte    `json:"payload"`
		ReceivedAt time.Time `json:"received_at"`
		StartedAt  time.Time `json:"started_at"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatalf("entry does not decode as a journalEntry: %v\n%s", err, raw)
	}
	if entry.Key != "post:clock-1" || !entry.StartedAt.IsZero() {
		t.Errorf("entry %+v", entry)
	}
	age := time.Since(entry.ReceivedAt)
	if age < 110*time.Second || age > 130*time.Second {
		t.Errorf("received_at is %s old, expected ~120 s", age.Round(time.Second))
	}
	var msg struct {
		PostID   string `json:"post_id"`
		Commands string `json:"commands"`
	}
	if err := json.Unmarshal(entry.Payload, &msg); err != nil || msg.PostID != "clock-1" {
		t.Fatalf("payload is not the C2D message: %v %s", err, entry.Payload)
	}
	utf16, err := base64.StdEncoding.DecodeString(msg.Commands)
	if err != nil {
		t.Fatal(err)
	}
	var decoded []byte
	for i := 0; i+1 < len(utf16); i += 2 {
		decoded = append(decoded, utf16[i])
		if utf16[i+1] != 0 {
			t.Fatalf("commands are not UTF-16LE: %x", utf16)
		}
	}
	if string(decoded) != "echo ok" {
		t.Errorf("commands decode to %q", decoded)
	}
	hb, err := os.ReadFile(filepath.Join(data, "command_journal", "heartbeat"))
	if err != nil {
		t.Fatal(err)
	}
	stamp, err := time.Parse(time.RFC3339Nano, string(hb))
	if err != nil || time.Since(stamp) > time.Minute {
		t.Errorf("heartbeat %q: %v", hb, err)
	}
	offset, err := os.ReadFile(filepath.Join(data, "clock_offset_override"))
	if err != nil || string(offset) != "25h" {
		t.Errorf("clock offset override %q: %v", offset, err)
	}
	code, _ = run(t, "hermetic-seed-journal.ps1",
		"DATA_DIR="+data, "POST_ID=clock-2", "COMMANDS=echo ok",
		"RECEIVED_AGO_SECONDS=1", "CLOCK_OFFSET=")
	if code != 0 {
		t.Fatal("re-seeding without an offset failed")
	}
	if _, err := os.Stat(filepath.Join(data, "clock_offset_override")); !os.IsNotExist(err) {
		t.Error("an empty CLOCK_OFFSET did not remove the override file")
	}
}

func TestEnqueue_HonoursThePostIDPrefix(t *testing.T) {
	s := newControlStub(t, nil)
	code, out := run(
		t,
		"hermetic-enqueue-commands.ps1",
		"ENGINE_URL="+s.srv.URL,
		"DEVICE_ID=dev-1",
		"COUNT=2",
		"COMMANDS=echo ok",
		"POST_ID_PREFIX=spool",
	)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	s.mu.Lock()
	enqueued := append([]map[string]any(nil), s.enqueued...)
	s.mu.Unlock()
	if len(enqueued) != 2 || enqueued[0]["post_id"] != "spool-1" ||
		enqueued[1]["post_id"] != "spool-2" {
		t.Errorf("enqueued %v", enqueued)
	}
}
