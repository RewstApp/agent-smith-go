package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/hashicorp/go-hclog"
)

// postbackPayload builds a message JSON with a post_id so processMessage
// attempts a postback.
func postbackPayload(commands, postID string) []byte {
	type msg struct {
		Commands string `json:"commands"`
		PostID   string `json:"post_id"`
	}
	b, _ := json.Marshal(msg{Commands: commands, PostID: postID})
	return b
}

func newProcessMessageSvc(exec *mockExecutor, httpClient *http.Client) *serviceContext {
	return &serviceContext{
		Executor:                 exec,
		Sys:                      &mockSystemInfoProvider{hostname: "host", hostPlatform: "linux"},
		Domain:                   &mockDomainInfoProvider{},
		HTTPClient:               httpClient,
		PostbackMaxAttempts:      postbackMaxAttempts,
		PostbackBaseRetryBackoff: time.Millisecond,
	}
}

// deviceWithEngine returns a Device whose RewstEngineHost points to host
// (stripped of scheme) so CreatePostbackRequest builds a valid URL.
func deviceWithEngine(host string) agent.Device {
	return agent.Device{
		RewstEngineHost: host,
		RewstOrgId:      "test-org",
	}
}

// TestProcessMessage_NoPostId verifies that a message without a post_id
// executes but does not attempt a postback.
func TestProcessMessage_NoPostId(t *testing.T) {
	exec := &mockExecutor{}
	svc := newProcessMessageSvc(exec, nil)

	ctx := context.Background()
	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	device := agent.Device{}

	svc.processMessage(validPayload("echo hi"), ctx, device, logger, notifier)

	if !exec.executeCalled {
		t.Error("expected Executor.Execute to be called")
	}
}

// TestProcessMessage_InvalidJSON verifies that a malformed payload is handled
// without a panic.
func TestProcessMessage_InvalidJSON(t *testing.T) {
	exec := &mockExecutor{}
	svc := newProcessMessageSvc(exec, nil)

	ctx := context.Background()
	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	device := agent.Device{}

	// Should log an error and return without panicking.
	svc.processMessage([]byte("not-json"), ctx, device, logger, notifier)

	if exec.executeCalled {
		t.Error("expected Executor.Execute NOT to be called for invalid payload")
	}
}

// TestProcessMessage_PostbackSuccess verifies the happy-path postback.
func TestProcessMessage_PostbackSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Strip scheme — RewstEngineHost is used as a bare host in the URL.
	host := srv.Listener.Addr().String()

	exec := &mockExecutor{result: []byte(`{}`)}
	svc := newProcessMessageSvc(exec, &http.Client{
		Transport: &http.Transport{},
	})

	// Override the postback URL scheme to http so our test server is reachable.
	// We do this by pointing RewstEngineHost to the test server's address and
	// temporarily swapping the scheme by using a RoundTripper that rewrites https→http.
	svc.HTTPClient = &http.Client{
		Transport: &schemeRewriteTransport{scheme: "http"},
	}

	ctx := context.Background()
	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	device := deviceWithEngine(host)

	svc.processMessage(postbackPayload("echo hi", "id:123"), ctx, device, logger, notifier)

	if !exec.executeCalled {
		t.Error("expected Executor.Execute to be called")
	}
}

// TestProcessMessage_PostbackDisabled verifies that DisableAgentPostback
// skips the postback when AlwaysPostback is false.
func TestProcessMessage_PostbackDisabled(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exec := &mockExecutor{result: []byte(`{}`)}
	svc := newProcessMessageSvc(exec, nil)

	ctx := context.Background()
	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	device := agent.Device{
		RewstEngineHost:      srv.Listener.Addr().String(),
		DisableAgentPostback: true,
	}

	svc.processMessage(postbackPayload("echo hi", "id:123"), ctx, device, logger, notifier)

	if called {
		t.Error("expected postback NOT to be sent when DisableAgentPostback is true")
	}
}

// TestProcessMessage_PostbackHttpError verifies a network failure on postback
// is handled without a panic.
func TestProcessMessage_PostbackHttpError(t *testing.T) {
	exec := &mockExecutor{result: []byte(`{}`)}
	svc := newProcessMessageSvc(exec, &http.Client{
		Transport: &schemeRewriteTransport{scheme: "http"},
	})

	ctx := context.Background()
	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	// Point to an unreachable address.
	device := deviceWithEngine("127.0.0.1:1")

	svc.processMessage(postbackPayload("echo hi", "id:err"), ctx, device, logger, notifier)
}

// TestProcessMessage_PostbackNon200 verifies a non-200 postback response is
// handled without a panic.
func TestProcessMessage_PostbackNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"server error"}`))
	}))
	defer srv.Close()

	exec := &mockExecutor{result: []byte(`{}`)}
	svc := newProcessMessageSvc(exec, &http.Client{
		Transport: &schemeRewriteTransport{scheme: "http"},
	})

	ctx := context.Background()
	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	device := deviceWithEngine(srv.Listener.Addr().String())

	svc.processMessage(postbackPayload("echo hi", "id:500"), ctx, device, logger, notifier)
}

// TestProcessMessage_PostbackFulfilled verifies the "already fulfilled"
// (400 + "fulfilled") response is handled without a panic.
func TestProcessMessage_PostbackFulfilled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"webhook already fulfilled"}`))
	}))
	defer srv.Close()

	exec := &mockExecutor{result: []byte(`{}`)}
	svc := newProcessMessageSvc(exec, &http.Client{
		Transport: &schemeRewriteTransport{scheme: "http"},
	})

	ctx := context.Background()
	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	device := deviceWithEngine(srv.Listener.Addr().String())

	svc.processMessage(postbackPayload("echo hi", "id:fulfilled"), ctx, device, logger, notifier)
}

// schemeRewriteTransport rewrites the request scheme before forwarding,
// allowing tests to hit plain-HTTP servers when processMessage builds https URLs.
type schemeRewriteTransport struct {
	scheme string
}

func (t *schemeRewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.URL.Scheme = t.scheme
	return http.DefaultTransport.RoundTrip(r)
}

// TestProcessMessage_PostbackRetriesOnServerError verifies that a transient
// 5xx response is retried and that a later success delivers the result.
func TestProcessMessage_PostbackRetriesOnServerError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if n < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"transient"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exec := &mockExecutor{result: []byte(`{}`)}
	svc := newProcessMessageSvc(exec, &http.Client{
		Transport: &schemeRewriteTransport{scheme: "http"},
	})

	ctx := context.Background()
	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	device := deviceWithEngine(srv.Listener.Addr().String())

	svc.processMessage(postbackPayload("echo hi", "id:retry-5xx"), ctx, device, logger, notifier)

	if got := calls.Load(); got != 3 {
		t.Errorf("expected 3 postback attempts, got %d", got)
	}
}

// TestProcessMessage_PostbackRetriesOnNetworkError verifies that a transient
// network failure is retried and that a later success delivers the result.
func TestProcessMessage_PostbackRetriesOnNetworkError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Fail the first two requests at the transport layer; let the third
	// pass through to the live test server.
	failing := &failingThenPassTransport{
		failures: 2,
		fallback: &schemeRewriteTransport{scheme: "http"},
	}

	exec := &mockExecutor{result: []byte(`{}`)}
	svc := newProcessMessageSvc(exec, &http.Client{Transport: failing})

	ctx := context.Background()
	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	device := deviceWithEngine(srv.Listener.Addr().String())

	svc.processMessage(postbackPayload("echo hi", "id:retry-net"), ctx, device, logger, notifier)

	if got := failing.attempts.Load(); got != 3 {
		t.Errorf("expected 3 transport attempts, got %d", got)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected exactly one server-side delivery, got %d", got)
	}
}

// TestProcessMessage_PostbackExhaustsRetries verifies that when every attempt
// fails the loop stops after the configured maximum and surfaces the failure.
func TestProcessMessage_PostbackExhaustsRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"bad gateway"}`))
	}))
	defer srv.Close()

	exec := &mockExecutor{result: []byte(`{}`)}
	svc := newProcessMessageSvc(exec, &http.Client{
		Transport: &schemeRewriteTransport{scheme: "http"},
	})

	ctx := context.Background()
	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	device := deviceWithEngine(srv.Listener.Addr().String())

	svc.processMessage(postbackPayload("echo hi", "id:exhaust"), ctx, device, logger, notifier)

	if got := calls.Load(); int(got) != svc.PostbackMaxAttempts {
		t.Errorf("expected %d attempts before giving up, got %d", svc.PostbackMaxAttempts, got)
	}
}

// TestProcessMessage_PostbackSuccessFirstAttemptNoRetry verifies that a
// first-attempt 200 OK does not trigger any additional requests.
func TestProcessMessage_PostbackSuccessFirstAttemptNoRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exec := &mockExecutor{result: []byte(`{}`)}
	svc := newProcessMessageSvc(exec, &http.Client{
		Transport: &schemeRewriteTransport{scheme: "http"},
	})

	ctx := context.Background()
	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	device := deviceWithEngine(srv.Listener.Addr().String())

	start := time.Now()
	svc.processMessage(postbackPayload("echo hi", "id:fast"), ctx, device, logger, notifier)
	elapsed := time.Since(start)

	if got := calls.Load(); got != 1 {
		t.Errorf("expected exactly one postback on first-try success, got %d", got)
	}
	// First-attempt success must not pay any backoff cost. The base backoff
	// is only 1ms in tests but real-world is seconds — assert that the call
	// returns well below a single backoff window.
	if elapsed > 100*time.Millisecond {
		t.Errorf("first-attempt success took unexpectedly long: %v", elapsed)
	}
}

// TestProcessMessage_PostbackTerminalOn4xxNoRetry verifies that a 4xx
// response (with a parseable error body and not "fulfilled") is treated as
// terminal and not retried.
func TestProcessMessage_PostbackTerminalOn4xxNoRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"malformed request"}`))
	}))
	defer srv.Close()

	exec := &mockExecutor{result: []byte(`{}`)}
	svc := newProcessMessageSvc(exec, &http.Client{
		Transport: &schemeRewriteTransport{scheme: "http"},
	})

	ctx := context.Background()
	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	device := deviceWithEngine(srv.Listener.Addr().String())

	svc.processMessage(postbackPayload("echo hi", "id:4xx"), ctx, device, logger, notifier)

	if got := calls.Load(); got != 1 {
		t.Errorf("expected 4xx to be terminal (1 attempt), got %d", got)
	}
}

// TestProcessMessage_PostbackContextCancelStopsRetries verifies that a
// cancelled context aborts the retry loop instead of waiting out the backoff.
func TestProcessMessage_PostbackContextCancelStopsRetries(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"down"}`))
	}))
	defer srv.Close()

	exec := &mockExecutor{result: []byte(`{}`)}
	svc := newProcessMessageSvc(exec, &http.Client{
		Transport: &schemeRewriteTransport{scheme: "http"},
	})
	// Use a longer backoff so the cancellation observably short-circuits the wait.
	svc.PostbackBaseRetryBackoff = 10 * time.Second

	ctx, cancel := context.WithCancel(context.Background())
	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	device := deviceWithEngine(srv.Listener.Addr().String())

	// Cancel after a short delay so the first attempt completes but the
	// retry-backoff sleep is interrupted.
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	svc.processMessage(postbackPayload("echo hi", "id:cancel"), ctx, device, logger, notifier)
	elapsed := time.Since(start)

	if elapsed >= 5*time.Second {
		t.Errorf("expected retry loop to abort on cancel, elapsed=%v", elapsed)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("expected exactly one server attempt before cancel, got %d", got)
	}
}

// failingThenPassTransport fails the first N requests with a transport error
// and then delegates to fallback. The total number of round-trips it has
// observed is exposed via attempts for assertions.
type failingThenPassTransport struct {
	failures int32
	attempts atomic.Int32
	fallback http.RoundTripper
}

func (t *failingThenPassTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	n := t.attempts.Add(1)
	if n <= t.failures {
		return nil, &simulatedNetError{msg: "simulated transport failure"}
	}
	return t.fallback.RoundTrip(req)
}

type simulatedNetError struct{ msg string }

func (e *simulatedNetError) Error() string { return e.msg }
