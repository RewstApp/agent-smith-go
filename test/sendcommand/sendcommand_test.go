// Package sendcommand runs .github/actions/send-command/send-command.sh
// against stub engines and pins every response class the script documents:
// which classes retry, which fail on the first attempt, what the annotation
// says, and how many requests reach the engine. It runs under a plain
// `go test ./...` so the test workflow exercises it on every platform that
// has bash and curl (Git Bash on windows-latest).
package sendcommand

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

const (
	timeoutBody = `{"error":"The workflow did not complete in a reasonable amount of time to receive results.","execution_id":"01a0-test"}`
	goodBody    = `{"output":{"output":{"device_execution_results":"","command_results":{"error":"","output":"hello world\n"}},"errors":[],"options":[]}}`
	otherBody   = `{"output":{"output":{"device_execution_results":"","command_results":{"error":"","output":"something else\n"}},"errors":[],"options":[]}}`
	noResults   = `{"output":{"output":{"device_execution_results":""}},"errors":[],"options":[]}`
	nginx502    = "<html>\n<head><title>502 Bad Gateway</title></head>\n<body>\n<center><h1>502 Bad Gateway</h1></center>\n</body>\n</html>"
	notFound    = `{"error":"Workflow was not found"}`
)

type response struct {
	status int
	body   string
}

// stubEngine answers each request with the next scripted response and repeats
// the last one if the script runs out, counting requests.
type stubEngine struct {
	mu        sync.Mutex
	responses []response
	requests  int
	server    *httptest.Server
}

func newStubEngine(t *testing.T, responses ...response) *stubEngine {
	t.Helper()
	s := &stubEngine{responses: responses}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		i := s.requests
		if i >= len(s.responses) {
			i = len(s.responses) - 1
		}
		s.requests++
		w.WriteHeader(s.responses[i].status)
		_, _ = w.Write([]byte(s.responses[i].body))
	}))
	t.Cleanup(s.server.Close)
	return s
}

func (s *stubEngine) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests
}

func bashPath(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		// The composite action runs under Git Bash; WSL's bash.exe in System32
		// would fail without a distribution, so look for Git's first.
		for _, p := range []string{
			`C:\Program Files\Git\bin\bash.exe`,
			`C:\Program Files (x86)\Git\bin\bash.exe`,
		} {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	p, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available; the send-command action cannot run here")
	}
	return p
}

func scriptPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs(
		filepath.Join("..", "..", ".github", "actions", "send-command", "send-command.sh"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("send-command.sh not found at %s: %v", p, err)
	}
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl not available; the send-command action cannot run here")
	}
	return p
}

// run executes the script with the given trigger URL and extra environment,
// returning its exit code and combined output. Retries are made instant.
func run(t *testing.T, triggerURL string, extraEnv ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(bashPath(t), scriptPath(t))
	cmd.Env = append(os.Environ(),
		"TRIGGER_URL="+triggerURL,
		"DEVICE_ID=00000000-0000-0000-0000-000000000000",
		`COMMANDS="echo hello world"`,
		"MAX_TIME=20",
		"RETRY_ATTEMPTS=3",
		"RETRY_DELAY=0",
	)
	cmd.Env = append(cmd.Env, extraEnv...)
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("running script: %v\n%s", err, out)
		}
		code = exitErr.ExitCode()
	}
	return code, string(out)
}

func wantContains(t *testing.T, out string, substrings ...string) {
	t.Helper()
	for _, s := range substrings {
		if !strings.Contains(out, s) {
			t.Errorf("output missing %q\n--- output ---\n%s", s, out)
		}
	}
}

func wantNotContains(t *testing.T, out string, substrings ...string) {
	t.Helper()
	for _, s := range substrings {
		if strings.Contains(out, s) {
			t.Errorf("output unexpectedly contains %q\n--- output ---\n%s", s, out)
		}
	}
}

func TestSuccess_MatchingResultIsDispatchedOnFirstAttempt(t *testing.T) {
	engine := newStubEngine(t, response{200, goodBody})
	code, out := run(t, engine.server.URL, "EXPECTED_OUTPUT=hello world")
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, out)
	}
	wantContains(t, out, "class: success", "attempt 1 of 3")
	wantNotContains(t, out, "::warning", "::error")
	if n := engine.count(); n != 1 {
		t.Errorf("engine saw %d requests, want 1", n)
	}
}

func TestSuccess_WithoutExpectedOutputAnyCommandResultsPass(t *testing.T) {
	engine := newStubEngine(t, response{200, otherBody})
	code, out := run(t, engine.server.URL)
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, out)
	}
	wantContains(t, out, "class: success")
}

func TestEngineTransient_502ThenSuccessRetriesOnceWithWarning(t *testing.T) {
	engine := newStubEngine(t, response{502, nginx502}, response{200, goodBody})
	code, out := run(t, engine.server.URL, "EXPECTED_OUTPUT=hello world")
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, out)
	}
	wantContains(t, out,
		"::warning title=send-command: engine-transient, retrying::attempt 1 of 3 got HTTP 502",
		"class: success",
		"attempt 2 of 3",
	)
	wantNotContains(t, out, "::error")
	if n := engine.count(); n != 2 {
		t.Errorf("engine saw %d requests, want 2", n)
	}
}

func TestEngineTransient_404WorkflowNotFoundIsRetried(t *testing.T) {
	engine := newStubEngine(t, response{404, notFound}, response{200, goodBody})
	code, out := run(t, engine.server.URL, "EXPECTED_OUTPUT=hello world")
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, out)
	}
	wantContains(
		t,
		out,
		"engine-transient, retrying::attempt 1 of 3 got HTTP 404",
		"class: success",
	)
	if n := engine.count(); n != 2 {
		t.Errorf("engine saw %d requests, want 2", n)
	}
}

func TestEngineTransient_ExhaustedFailsNamingTheClass(t *testing.T) {
	engine := newStubEngine(t, response{503, "upstream unavailable"})
	code, out := run(t, engine.server.URL)
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	wantContains(
		t,
		out,
		"engine-transient, retrying::attempt 1 of 3",
		"engine-transient, retrying::attempt 2 of 3",
		"::error title=send-command: engine-transient::the engine answered HTTP 503 on all 3 attempts",
		"NOT a regression in the code under test",
	)
	if n := engine.count(); n != 3 {
		t.Errorf("engine saw %d requests, want 3", n)
	}
}

func TestEngineRefusal_400FailsOnFirstAttempt(t *testing.T) {
	engine := newStubEngine(t, response{400, `{"error":"bad payload"}`}, response{200, goodBody})
	code, out := run(t, engine.server.URL)
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	wantContains(
		t,
		out,
		"::error title=send-command: engine-refusal::the Rewst engine returned HTTP 400",
	)
	wantNotContains(t, out, "::warning")
	if n := engine.count(); n != 1 {
		t.Errorf("engine saw %d requests, want 1 (a refusal must not be retried)", n)
	}
}

func TestEngineRefusal_404WithoutTheTransientBodyIsARefusal(t *testing.T) {
	engine := newStubEngine(
		t,
		response{404, `{"error":"no such trigger"}`},
		response{200, goodBody},
	)
	code, out := run(t, engine.server.URL)
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	wantContains(t, out, "engine-refusal::the Rewst engine returned HTTP 404")
	if n := engine.count(); n != 1 {
		t.Errorf("engine saw %d requests, want 1", n)
	}
}

func TestEngineTimeout_408ThenSuccessRetries(t *testing.T) {
	engine := newStubEngine(t, response{408, timeoutBody}, response{200, goodBody})
	code, out := run(t, engine.server.URL, "EXPECTED_OUTPUT=hello world")
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, out)
	}
	wantContains(
		t,
		out,
		"engine-timeout, retrying::attempt 1 of 3",
		"execution_id=01a0-test",
		"class: success",
	)
}

func TestEngineTimeout_2xxTimeoutBodyExhaustedFails(t *testing.T) {
	engine := newStubEngine(t, response{200, timeoutBody})
	code, out := run(t, engine.server.URL)
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	wantContains(
		t,
		out,
		"::error title=send-command: engine-timeout::the engine gave up waiting for a result on all 3 attempts",
	)
	if n := engine.count(); n != 3 {
		t.Errorf("engine saw %d requests, want 3", n)
	}
}

func TestEngineTimeout_AllowedPassesWithNoticeAndNoRetry(t *testing.T) {
	engine := newStubEngine(t, response{200, timeoutBody})
	code, out := run(t, engine.server.URL, "ALLOW_ENGINE_TIMEOUT=true")
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, out)
	}
	wantContains(t, out, "::notice title=send-command: engine-timeout (expected here)")
	if n := engine.count(); n != 1 {
		t.Errorf("engine saw %d requests, want 1", n)
	}
}

func TestWrongResult_MismatchedOutputFailsAtThisStep(t *testing.T) {
	engine := newStubEngine(t, response{200, otherBody})
	code, out := run(t, engine.server.URL, "EXPECTED_OUTPUT=hello world")
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	wantContains(t, out,
		"::error title=send-command: wrong-result::HTTP 200 with command_results.output",
		"does not contain the expected",
		"another agent on the same device_id",
	)
	if n := engine.count(); n != 1 {
		t.Errorf("engine saw %d requests, want 1 (a wrong result must not be retried)", n)
	}
}

func TestWrongResult_BodyWithoutCommandResultsFails(t *testing.T) {
	engine := newStubEngine(t, response{200, noResults})
	code, out := run(t, engine.server.URL)
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	wantContains(t, out, "wrong-result::HTTP 200 but the body carries no command_results object")
}

func TestRequestFailed_ConnectionRefusedIsNotRetried(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()

	code, out := run(t, "http://"+addr+"/trigger")
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	wantContains(t, out, "::error title=send-command: request-failed::curl exited")
	wantNotContains(t, out, "::warning")
}

func TestRetryAttempts_OneDisablesRetryForTransients(t *testing.T) {
	engine := newStubEngine(t, response{502, nginx502}, response{200, goodBody})
	code, out := run(t, engine.server.URL, "RETRY_ATTEMPTS=1")
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	wantContains(t, out, "engine-transient::the engine answered HTTP 502 on all 1 attempts")
	if n := engine.count(); n != 1 {
		t.Errorf("engine saw %d requests, want 1", n)
	}
}

func TestWrongResult_FallbackWithoutJqStillMatchesAndStillRejects(t *testing.T) {
	good := newStubEngine(t, response{200, goodBody})
	code, out := run(
		t,
		good.server.URL,
		"EXPECTED_OUTPUT=hello world",
		"SEND_COMMAND_FORCE_NO_JQ=1",
	)
	if code != 0 {
		t.Fatalf("fallback rejected a matching result: exit %d\n%s", code, out)
	}
	wantContains(t, out, "class: success")

	other := newStubEngine(t, response{200, otherBody})
	code, out = run(
		t,
		other.server.URL,
		"EXPECTED_OUTPUT=hello world",
		"SEND_COMMAND_FORCE_NO_JQ=1",
	)
	if code != 1 {
		t.Fatalf("fallback accepted a foreign result: exit %d\n%s", code, out)
	}
	wantContains(t, out, "wrong-result::HTTP 200 with command_results.output")
}

// A 408 means the engine stopped waiting, not that the device never got the
// command (sc-115631's step-78 investigation). The retry therefore dispatches a
// duplicate, and the engine's next answer can be the first copy's postback -
// run 35742735431 returned the agent's own empty-stdout duplicate. After a
// timeout in the same step that is a warning, not a failure; the log assertion
// after the send still checks the device did the work.
func TestWrongResult_AfterEngineTimeoutRetryIsAWarningNotAFailure(t *testing.T) {
	engine := newStubEngine(t, response{408, timeoutBody}, response{200, otherBody})
	code, out := run(t, engine.server.URL, "EXPECTED_OUTPUT=hello world")
	if code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, out)
	}
	wantContains(
		t,
		out,
		"engine-timeout, retrying::attempt 1 of 3: the engine gave up waiting",
		"::warning title=send-command: wrong-result after engine-timeout retry::HTTP 200 with command_results.output",
		"class: success-after-timeout",
	)
	wantNotContains(t, out, "::error")
	if n := engine.count(); n != 2 {
		t.Errorf("engine saw %d requests, want 2", n)
	}
}

// The relaxation is only for the output match. A 2xx that is not even shaped
// like a device postback is still wrong-result after a timeout.
func TestWrongResult_NoCommandResultsStillFailsAfterEngineTimeoutRetry(t *testing.T) {
	engine := newStubEngine(t, response{408, timeoutBody}, response{200, noResults})
	code, out := run(t, engine.server.URL, "EXPECTED_OUTPUT=hello world")
	if code != 1 {
		t.Fatalf("exit %d, want 1\n%s", code, out)
	}
	wantContains(t, out, "wrong-result::HTTP 200 but the body carries no command_results object")
}

// And without a prior timeout the mismatch is still the hard failure it was.
func TestWrongResult_MismatchWithoutPriorTimeoutStillFails(t *testing.T) {
	engine := newStubEngine(t, response{502, nginx502}, response{200, otherBody})
	code, out := run(t, engine.server.URL, "EXPECTED_OUTPUT=hello world")
	if code != 1 {
		t.Fatalf("exit %d, want 1 (a transient retry is not a timeout retry)\n%s", code, out)
	}
	wantContains(t, out, "::error title=send-command: wrong-result::")
}
