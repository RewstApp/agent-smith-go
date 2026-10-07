// Package hermeticscripts runs the two PowerShell scripts the hermetic
// integration job uses against a stub of the fixture's control surface, under
// a plain `go test ./...`, so a change to either script - or to the shape the
// fixture expects - fails here rather than seven minutes into a CI run.
//
// Both scripts talk to test/stubbroker's stand-in engine: the enqueue script
// owes the device commands through /_control/enqueue, and the assert script
// reads what came back from /_control/postbacks. The stub here records the
// requests the first makes and serves a fixed list to the second. Tests skip
// when pwsh is not installed.
package hermeticscripts

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type postback struct {
	PostID string          `json:"post_id"`
	Body   json.RawMessage `json:"body"`
}

type controlStub struct {
	mu        sync.Mutex
	enqueued  []map[string]any
	postbacks []postback
	srv       *httptest.Server
}

func newControlStub(t *testing.T, postbacks []postback) *controlStub {
	t.Helper()
	s := &controlStub{postbacks: postbacks}
	mux := http.NewServeMux()
	mux.HandleFunc("/_control/enqueue", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.mu.Lock()
		s.enqueued = append(s.enqueued, req)
		n := len(s.enqueued)
		s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": fmt.Sprintf("stub-%d", n)})
	})
	mux.HandleFunc("/_control/postbacks", func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(s.postbacks)
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func pwshPath(t *testing.T) string {
	t.Helper()
	p, err := exec.LookPath("pwsh")
	if err != nil {
		t.Skip("pwsh not available; the hermetic job's scripts cannot run here")
	}
	return p
}

func scriptPath(t *testing.T, name string) string {
	t.Helper()
	p, err := filepath.Abs(filepath.Join("..", "..", ".github", "workflows", "it-scripts", name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("%s not found at %s: %v", name, p, err)
	}
	return p
}

// run executes a script the way the workflow does (`pwsh -command ". '<path>'"`)
// and returns its exit code and combined output.
func run(t *testing.T, name string, env ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(pwshPath(t), "-NoProfile", "-Command", ". '"+scriptPath(t, name)+"'")
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running %s: %v\n%s", name, err, out)
		}
		code = exitErr.ExitCode()
	}
	return code, string(out)
}

func hermeticPostbacks(n int, interrupted ...string) []postback {
	// Two foreign results first, as the real run has them: the success and
	// failing commands the job round-trips before it queues the hermetic ones.
	out := []postback{
		{
			PostID: "f515a7320083308ff03f0807a640cc2f",
			Body:   json.RawMessage(`{"output":"hello world\n","error":""}`),
		},
		{
			PostID: "325b13d8053e670280def38d7960dee4",
			Body:   json.RawMessage(`{"output":"","error":""}`),
		},
	}
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("hermetic-%d", i)
		body := `{"output":"","error":""}`
		for _, want := range interrupted {
			if want == id {
				body = `{"error":"command interrupted: agent stop","output":"","interrupted":true}`
			}
		}
		out = append(out, postback{PostID: id, Body: json.RawMessage(body)})
	}
	return out
}

func TestEnqueue_SendsPostIDAndCommandsAtTheTopLevel(t *testing.T) {
	stub := newControlStub(t, nil)
	code, out := run(t, "hermetic-enqueue-commands.ps1",
		"ENGINE_URL="+stub.srv.URL,
		"DEVICE_ID=hermetic-test-device",
		`COMMANDS="sleep 30"`,
		"COUNT=3",
	)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	stub.mu.Lock()
	defer stub.mu.Unlock()
	if len(stub.enqueued) != 3 {
		t.Fatalf("enqueued %d requests, want 3\n%s", len(stub.enqueued), out)
	}
	for i, req := range stub.enqueued {
		wantID := fmt.Sprintf("hermetic-%d", i+1)
		if req["device_id"] != "hermetic-test-device" {
			t.Errorf("request %d device_id = %v", i+1, req["device_id"])
		}
		if req["post_id"] != wantID {
			t.Errorf("request %d post_id = %v, want %s", i+1, req["post_id"], wantID)
		}
		// The matrix passes the command JSON-quoted; the fixture takes the bare
		// string at the top level and encodes it as the real engine does. A
		// raw `payload` would bypass that encoding and the agent would reject
		// the plain text as illegal base64.
		if req["commands"] != "sleep 30" {
			t.Errorf("request %d commands = %#v, want %q", i+1, req["commands"], "sleep 30")
		}
		if _, raw := req["payload"]; raw {
			t.Errorf("request %d carries a raw payload, which skips the fixture's encoding", i+1)
		}
		if !strings.Contains(out, fmt.Sprintf("enqueued %s as stub-%d", wantID, i+1)) {
			t.Errorf("output does not report %s:\n%s", wantID, out)
		}
	}
}

func TestAssert_SeesEveryReportInTheArrayTheEngineReturns(t *testing.T) {
	stub := newControlStub(t, hermeticPostbacks(5, "hermetic-3"))
	code, out := run(t, "hermetic-assert-postbacks.ps1",
		"ENGINE_URL="+stub.srv.URL, "COUNT=5", "TIMEOUT_SECONDS=10",
	)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{
		"reported: hermetic-1, hermetic-2, hermetic-3, hermetic-4, hermetic-5",
		"interrupted: hermetic-3",
		"all 5 commands reported exactly once; one interrupted, 4 completed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestAssert_ASingleReportIsStillSeen(t *testing.T) {
	stub := newControlStub(t, []postback{{
		PostID: "hermetic-1",
		Body: json.RawMessage(
			`{"error":"command interrupted: agent stop","output":"","interrupted":true}`,
		),
	}})
	code, out := run(t, "hermetic-assert-postbacks.ps1",
		"ENGINE_URL="+stub.srv.URL, "COUNT=1", "TIMEOUT_SECONDS=10",
	)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	if !strings.Contains(out, "all 1 commands reported exactly once") {
		t.Errorf("output lacks the success line:\n%s", out)
	}
}

func TestAssert_NamesTheMissingPostIDs(t *testing.T) {
	all := hermeticPostbacks(5, "hermetic-1")
	// Drop hermetic-3 and hermetic-5.
	kept := all[:0:0]
	for _, p := range all {
		if p.PostID != "hermetic-3" && p.PostID != "hermetic-5" {
			kept = append(kept, p)
		}
	}
	stub := newControlStub(t, kept)
	code, out := run(t, "hermetic-assert-postbacks.ps1",
		"ENGINE_URL="+stub.srv.URL, "COUNT=5", "TIMEOUT_SECONDS=1",
	)
	if code == 0 {
		t.Fatalf("expected a failure\n%s", out)
	}
	for _, want := range []string{
		"reported: hermetic-1, hermetic-2, hermetic-4",
		"postbacks never arrived for: hermetic-3, hermetic-5",
		"postbacks the engine holds: ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

func TestAssert_RequiresExactlyOneInterrupted(t *testing.T) {
	for name, interrupted := range map[string][]string{
		"none": nil,
		"two":  {"hermetic-2", "hermetic-4"},
	} {
		t.Run(name, func(t *testing.T) {
			stub := newControlStub(t, hermeticPostbacks(5, interrupted...))
			code, out := run(t, "hermetic-assert-postbacks.ps1",
				"ENGINE_URL="+stub.srv.URL, "COUNT=5", "TIMEOUT_SECONDS=10",
			)
			if code == 0 {
				t.Fatalf("expected a failure\n%s", out)
			}
			want := fmt.Sprintf(
				"expected exactly one interrupted result (the command running at the kill), got %d",
				len(interrupted),
			)
			if !strings.Contains(out, want) {
				t.Errorf("output lacks %q:\n%s", want, out)
			}
		})
	}
}

func TestAssert_ReportsTheControlSurfaceErrorWhenNothingArrives(t *testing.T) {
	// A closed port: every poll fails and the script must say why rather than
	// report an empty engine.
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	code, out := run(t, "hermetic-assert-postbacks.ps1",
		"ENGINE_URL="+url, "COUNT=1", "TIMEOUT_SECONDS=1",
	)
	if code == 0 {
		t.Fatalf("expected a failure\n%s", out)
	}
	if !strings.Contains(out, "last control-surface error: ") {
		t.Errorf("output lacks the control-surface error:\n%s", out)
	}
	if !strings.Contains(out, "postbacks never arrived for: hermetic-1") {
		t.Errorf("output lacks the missing id:\n%s", out)
	}
}
