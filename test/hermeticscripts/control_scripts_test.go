package hermeticscripts

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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

func TestAssertTwin_ReadsADottedPathAndFailsOnAMismatch(t *testing.T) {
	s := newEdgeStub(t)
	s.srv.Config.Handler.(*http.ServeMux).HandleFunc(
		"/_control/devices/dev-7/twin",
		func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"device_id":"dev-7","reported":{"agent_version":"v1.6.1",` +
				`"health":{"journal":{"pending":3,"degraded":true},"spool":{"dropped_attempts":1}},` +
				`"commands":{"in_flight":[{"post_id":"hold-1","elapsed_seconds":12}]}}}`))
		},
	)
	code, out := run(t, "hermetic-assert-twin.ps1",
		"ENGINE_URL="+s.srv.URL, "DEVICE_ID=dev-7", "PROPERTY=health.journal.degraded",
		"EXPECTED=True", "TIMEOUT_SECONDS=5")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	code, out = run(t, "hermetic-assert-twin.ps1",
		"ENGINE_URL="+s.srv.URL, "DEVICE_ID=dev-7", "PROPERTY=health.spool.dropped_attempts",
		"EXPECTED=1", "TIMEOUT_SECONDS=5")
	if code != 0 {
		t.Fatalf("numeric: exit %d\n%s", code, out)
	}
	code, out = run(t, "hermetic-assert-twin.ps1",
		"ENGINE_URL="+s.srv.URL, "DEVICE_ID=dev-7", "PROPERTY=health.journal.pending",
		"EXPECTED=0", "TIMEOUT_SECONDS=3")
	if code == 0 || !strings.Contains(out, "is '3', expected '0'") {
		t.Errorf("a mismatch passed (exit %d):\n%s", code, out)
	}
	// Array index, Count and a numeric floor (sc-129689).
	for _, tc := range []struct{ prop, expected, min string }{
		{"commands.in_flight[0].post_id", "hold-1", ""},
		{"commands.in_flight.Count", "1", ""},
		{"commands.in_flight[0].elapsed_seconds", "", "10"},
	} {
		env := []string{
			"ENGINE_URL=" + s.srv.URL,
			"DEVICE_ID=dev-7",
			"PROPERTY=" + tc.prop,
			"TIMEOUT_SECONDS=5",
		}
		if tc.min != "" {
			env = append(env, "MIN="+tc.min)
		} else {
			env = append(env, "EXPECTED="+tc.expected)
		}
		if code, out := run(t, "hermetic-assert-twin.ps1", env...); code != 0 {
			t.Errorf("%s: exit %d\n%s", tc.prop, code, out)
		}
	}
	code, out = run(t, "hermetic-assert-twin.ps1",
		"ENGINE_URL="+s.srv.URL, "DEVICE_ID=dev-7",
		"PROPERTY=commands.in_flight[0].elapsed_seconds", "MIN=60", "TIMEOUT_SECONDS=3")
	if code == 0 {
		t.Errorf("a value below MIN passed:\n%s", out)
	}
	code, _ = run(t, "hermetic-assert-twin.ps1",
		"ENGINE_URL="+s.srv.URL, "DEVICE_ID=dev-7",
		"PROPERTY=commands.in_flight[3].post_id", "EXPECTED=x", "TIMEOUT_SECONDS=3")
	if code == 0 {
		t.Error("an out-of-range index passed")
	}
}

// The sc-129688 helpers: hermetic-set-desired posts the desired properties to
// the fixture, hermetic-assert-config-value reads the agent's config file next
// to its log.
func TestSetDesired_PostsTheJSONObjectToTheDeviceControl(t *testing.T) {
	var mu sync.Mutex
	var gotPath, gotBody, gotType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotPath, gotBody, gotType = r.URL.Path, string(body), r.Header.Get("Content-Type")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device_id":"dev-9","version":3,"desired":{"worker_count":-1}}`))
	}))
	t.Cleanup(srv.Close)
	code, out := run(t, "hermetic-set-desired.ps1",
		"ENGINE_URL="+srv.URL, "DEVICE_ID=dev-9", `DESIRED={"worker_count": -1}`)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	mu.Lock()
	path, posted, contentType := gotPath, gotBody, gotType
	mu.Unlock()
	if path != "/_control/devices/dev-9/desired" ||
		!strings.HasPrefix(contentType, "application/json") {
		t.Errorf("posted to %s as %s", path, contentType)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(posted), &body); err != nil ||
		body["worker_count"] != float64(-1) {
		t.Errorf("body %q: %v", posted, err)
	}
	if !strings.Contains(out, "version 3") {
		t.Errorf("output does not name the version: %s", out)
	}
	code, out = run(t, "hermetic-set-desired.ps1",
		"ENGINE_URL="+srv.URL, "DEVICE_ID=dev-9", "DESIRED=not json")
	if code == 0 {
		t.Errorf("a malformed DESIRED was posted:\n%s", out)
	}
}

func TestAssertConfigValue_ReadsTheConfigBesideTheLog(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "rewst_agent.log")
	config := `{"command_timeout_seconds": 3, "logging_level": "debug", "disable_auto_updates": true}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"command_timeout_seconds": "3",
		"logging_level":           "debug",
		"disable_auto_updates":    "True",
	} {
		code, out := run(t, "hermetic-assert-config-value.ps1",
			"LOG_FILE="+logFile, "KEY="+key, "EXPECTED="+want)
		if code != 0 {
			t.Errorf("%s: exit %d\n%s", key, code, out)
		}
	}
	code, out := run(t, "hermetic-assert-config-value.ps1",
		"LOG_FILE="+logFile, "KEY=command_timeout_seconds", "EXPECTED=120")
	if code == 0 || !strings.Contains(out, "expected '120'") {
		t.Errorf("a mismatch passed: exit %d\n%s", code, out)
	}
}

// hermetic-assert-diagnostic pipes the menu choices into the agent's
// --diagnostic mode and checks its output (sc-131253). A PowerShell script
// stands in for the agent binary so the test runs on every platform.
func TestAssertDiagnostic_PipesTheMenuAndChecksTheOutput(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "fake-agent.ps1")
	script := `$in = @($input)
Write-Output "args: $($args -join ' ')"
Write-Output "menu: $($in -join ',')"
Write-Output "  Fleet Health"
Write-Output "    commands: executing=0 completed_total=2 timed_out_total=1 failed_total=0"
`
	if err := os.WriteFile(fake, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out := run(t, "hermetic-assert-diagnostic.ps1",
		"AGENT_BINARY="+fake, "ORG_ID=org-1", "MENU=8,0",
		"PATTERNS=Fleet Health\ncompleted_total=2 timed_out_total=1\nmenu: 8,0\nargs: --org-id org-1 --diagnostic")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	code, out = run(t, "hermetic-assert-diagnostic.ps1",
		"AGENT_BINARY="+fake, "ORG_ID=org-1", "MENU=8,0",
		"PATTERNS=Fleet Health\ninterrupted_total=1")
	if code == 0 || !strings.Contains(out, "interrupted_total=1") {
		t.Errorf("a missing line passed: exit %d\n%s", code, out)
	
	}
	// No pattern at all, and a menu that does not end in exit, are refused
	// before the agent runs.
	for _, env := range [][]string{
		{"MENU=8,0", "PATTERNS="},
		{"MENU=8,0", "PATTERNS=\n\n"},
		{"MENU=8", "PATTERNS=Fleet Health"},
	} {
		code, out := run(t, "hermetic-assert-diagnostic.ps1",
			append([]string{"AGENT_BINARY=" + fake, "ORG_ID=org-1"}, env...)...)
		if code == 0 {
			t.Errorf("%v passed:\n%s", env, out)
		}
	}
}
