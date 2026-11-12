package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/interpreter"
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

// TestProcessMessage_PostbackExhaustionSpoolsAndNotifies verifies that when the
// in-line retry budget is exhausted the result is (a) surfaced via an
// AgentPostbackFailed plugin notification carrying the post_id and (b) persisted
// to the on-disk spool for later delivery rather than dropped.
func TestProcessMessage_PostbackExhaustionSpoolsAndNotifies(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"error":"down"}`))
	}))
	defer srv.Close()

	exec := &mockExecutor{result: []byte(`{"ok":true}`)}
	svc := newProcessMessageSvc(exec, &http.Client{
		Transport: &schemeRewriteTransport{scheme: "http"},
	})
	svc.spool = newPostbackSpool(
		t.TempDir(), 10, time.Hour, defaultSpoolMaxAttempts, hclog.NewNullLogger(),
	)

	ctx := context.Background()
	logger := hclog.NewNullLogger()
	notifier := &recordingNotifierWrapper{}
	device := deviceWithEngine(srv.Listener.Addr().String())

	svc.processMessage(
		postbackPayload("echo hi", "id:exhaust-spool"),
		ctx,
		device,
		logger,
		notifier,
	)

	// The failure must be surfaced beyond the log.
	var notified bool
	for _, m := range notifier.all() {
		if m == "AgentPostbackFailed:id:exhaust-spool" {
			notified = true
		}
	}
	if !notified {
		t.Errorf("expected AgentPostbackFailed notification, got %v", notifier.all())
	}

	// The result must be persisted, not dropped.
	if n := countSpoolFiles(t, svc.spool.dir); n != 1 {
		t.Errorf("expected exhausted result spooled, got %d files", n)
	}
}

// TestFlushPostbackSpool_DeliversSpooledResult verifies that a spooled result is
// re-delivered (and removed) once the engine is reachable again.
func TestFlushPostbackSpool_DeliversSpooledResult(t *testing.T) {
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
	svc.spool = newPostbackSpool(
		t.TempDir(), 10, time.Hour, defaultSpoolMaxAttempts, hclog.NewNullLogger(),
	)

	if err := svc.spool.enqueue(spoolEntry{
		PostId:    "id:recover",
		Result:    []byte(`{}`),
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	device := deviceWithEngine(srv.Listener.Addr().String())
	svc.flushPostbackSpool(context.Background(), device, hclog.NewNullLogger(), nil)

	if got := calls.Load(); got != 1 {
		t.Errorf("expected exactly one re-delivery, got %d", got)
	}
	if n := countSpoolFiles(t, svc.spool.dir); n != 0 {
		t.Errorf("expected spool emptied after delivery, %d remain", n)
	}
}

// TestFlushPostbackSpool_RetainsOnEngineDown verifies that a spooled result is
// kept (not lost) when the engine is still unreachable during a flush.
func TestFlushPostbackSpool_RetainsOnEngineDown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"still down"}`))
	}))
	defer srv.Close()

	exec := &mockExecutor{result: []byte(`{}`)}
	svc := newProcessMessageSvc(exec, &http.Client{
		Transport: &schemeRewriteTransport{scheme: "http"},
	})
	svc.spool = newPostbackSpool(
		t.TempDir(), 10, time.Hour, defaultSpoolMaxAttempts, hclog.NewNullLogger(),
	)

	if err := svc.spool.enqueue(spoolEntry{
		PostId:    "id:still-down",
		Result:    []byte(`{}`),
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	device := deviceWithEngine(srv.Listener.Addr().String())
	svc.flushPostbackSpool(context.Background(), device, hclog.NewNullLogger(), nil)

	if n := countSpoolFiles(t, svc.spool.dir); n != 1 {
		t.Errorf("expected spooled result retained while engine down, %d remain", n)
	}
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

	svc.processMessage(postbackPayload("echo hi", "id:fast"), ctx, device, logger, notifier)

	// Exactly one request is the proof that first-attempt success paid no
	// backoff: a retry would show up as a second call. The elapsed-time bound
	// this used to carry (100ms) measured the runner, not the code (sc-119840).
	if got := calls.Load(); got != 1 {
		t.Errorf("expected exactly one postback on first-try success, got %d", got)
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
	firstServed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			defer close(firstServed)
		}
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

	// Cancel once the engine has served the first attempt, so the cancellation
	// lands in the retry backoff rather than at an arbitrary wall-clock offset.
	go func() {
		<-firstServed
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

const receivedMessagePrefix = "AgentReceivedMessage:"

// TestBuildReceivedMessageNotification_SmallPayloadVerbatim verifies that a
// payload at or below the threshold is embedded verbatim, preserving the
// existing notification format for normal-sized messages.
func TestBuildReceivedMessageNotification_SmallPayloadVerbatim(t *testing.T) {
	payload := []byte(`{"commands":"echo hi"}`)

	got := buildReceivedMessageNotification(payload)

	want := receivedMessagePrefix + string(payload)
	if got != want {
		t.Errorf("expected verbatim notification %q, got %q", want, got)
	}
}

// TestBuildReceivedMessageNotification_BoundaryVerbatim verifies that a payload
// exactly at the threshold is still sent verbatim (the boundary is inclusive).
func TestBuildReceivedMessageNotification_BoundaryVerbatim(t *testing.T) {
	payload := []byte(strings.Repeat("a", maxNotificationPayloadBytes))

	got := buildReceivedMessageNotification(payload)

	want := receivedMessagePrefix + string(payload)
	if got != want {
		t.Errorf("expected payload at threshold to be sent verbatim")
	}
	if strings.Contains(got, "truncated") {
		t.Errorf("payload at threshold must not be summarised, got %q", got)
	}
}

// TestBuildReceivedMessageNotification_LargePayloadBounded verifies that the
// notification size stays bounded for arbitrarily large payloads: the full
// payload is never embedded, and the result reports the true byte length plus a
// truncated prefix.
func TestBuildReceivedMessageNotification_LargePayloadBounded(t *testing.T) {
	// A payload well beyond the threshold (8 MiB) — the kind of multi-MB
	// workflow body that previously inflated agent memory.
	const payloadSize = 8 << 20
	payload := []byte(strings.Repeat("x", payloadSize))

	got := buildReceivedMessageNotification(payload)

	// The notification must be bounded by the prefix + summary text + the
	// truncated payload window, regardless of how large the payload is.
	maxLen := len(receivedMessagePrefix) + 64 + maxNotificationPayloadBytes
	if len(got) > maxLen {
		t.Errorf("notification length %d exceeds bound %d", len(got), maxLen)
	}
	// It must never embed the full payload.
	if len(got) >= payloadSize {
		t.Errorf("notification embeds full payload: len=%d, payload=%d", len(got), payloadSize)
	}
	if !strings.HasPrefix(got, receivedMessagePrefix) {
		t.Errorf("notification missing prefix: %q", got[:len(receivedMessagePrefix)])
	}
	// The true byte length must be reported so consumers can detect truncation.
	if !strings.Contains(got, "8388608") {
		t.Errorf("expected total byte length in summary, got %q", got[:128])
	}
}

// TestBuildReceivedMessageNotification_BoundedAcrossSizes asserts the bound
// holds for a range of payload sizes straddling the threshold.
func TestBuildReceivedMessageNotification_BoundedAcrossSizes(t *testing.T) {
	maxLen := len(receivedMessagePrefix) + 64 + maxNotificationPayloadBytes

	for _, size := range []int{0, 1, maxNotificationPayloadBytes - 1, maxNotificationPayloadBytes, maxNotificationPayloadBytes + 1, 1 << 20} {
		payload := []byte(strings.Repeat("z", size))
		got := buildReceivedMessageNotification(payload)

		if size <= maxNotificationPayloadBytes {
			if got != receivedMessagePrefix+string(payload) {
				t.Errorf("size %d: expected verbatim notification", size)
			}
			continue
		}
		if len(got) > maxLen {
			t.Errorf("size %d: notification length %d exceeds bound %d", size, len(got), maxLen)
		}
	}
}

// TestFlushPostbackSpool_PoisonedEntryDoesNotBlockOthers drives the sc-106112
// scenario through the real HTTP stack rather than a stubbed deliverer: an
// engine that rejects exactly one post_id with a 503 and answers 200 for every
// other. That is the case the old flush misread as an outage — the engine is
// plainly reachable, so every other spooled result must be delivered in the
// same flush.
func TestFlushPostbackSpool_PoisonedEntryDoesNotBlockOthers(t *testing.T) {
	var mu sync.Mutex
	var served []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The post id is the last path segment of the postback URL.
		id := path.Base(r.URL.Path)
		mu.Lock()
		served = append(served, id)
		mu.Unlock()

		if id == "poison" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"cannot accept this result"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	exec := &mockExecutor{result: []byte(`{}`)}
	svc := newProcessMessageSvc(exec, &http.Client{
		Transport: &schemeRewriteTransport{scheme: "http"},
	})
	svc.spool = newPostbackSpool(
		t.TempDir(), 10, time.Hour, defaultSpoolMaxAttempts, hclog.NewNullLogger(),
	)

	for _, id := range []string{"poison", "healthy-1", "healthy-2"} {
		if err := svc.spool.enqueue(spoolEntry{
			PostId:    id,
			Result:    []byte(`{}`),
			CreatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
		time.Sleep(time.Millisecond) // sleep-ok: distinct CreatedAt/filenames; ordering, not timing
	}

	device := deviceWithEngine(srv.Listener.Addr().String())
	svc.flushPostbackSpool(context.Background(), device, hclog.NewNullLogger(), nil)

	mu.Lock()
	got := append([]string(nil), served...)
	mu.Unlock()

	want := []string{"poison", "healthy-1", "healthy-2"}
	if len(got) != len(want) {
		t.Fatalf("expected the engine to receive all %d results, got %v", len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d: got post_id %q want %q", i, got[i], want[i])
		}
	}

	// Only the rejected entry is still spooled.
	if n := countSpoolFiles(t, svc.spool.dir); n != 1 {
		t.Errorf("expected only the rejected entry retained, %d files remain", n)
	}
}

// An entry the engine keeps rejecting is eventually abandoned, and that loss is
// surfaced to plugins the same way an exhausted in-line retry budget is — a
// result that never reached the engine must not vanish with only a log line.
func TestFlushPostbackSpool_AbandonedEntryNotifiesPlugins(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"cannot accept this result"}`))
	}))
	defer srv.Close()

	const maxAttempts = 2
	exec := &mockExecutor{result: []byte(`{}`)}
	svc := newProcessMessageSvc(exec, &http.Client{
		Transport: &schemeRewriteTransport{scheme: "http"},
	})
	svc.spool = newPostbackSpool(t.TempDir(), 10, time.Hour, maxAttempts, hclog.NewNullLogger())
	// Count both rejections rather than waiting out the production spacing; the
	// spacing itself is covered by TestSpool_RapidRejectionsDoNotBurnTheAttemptBudget.
	svc.spool.attemptInterval = 0

	if err := svc.spool.enqueue(spoolEntry{
		PostId:    "id:undeliverable",
		Result:    []byte(`{}`),
		CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	device := deviceWithEngine(srv.Listener.Addr().String())
	notifier := &recordingNotifierWrapper{}
	for range maxAttempts {
		svc.flushPostbackSpool(context.Background(), device, hclog.NewNullLogger(), notifier)
	}

	if n := countSpoolFiles(t, svc.spool.dir); n != 0 {
		t.Errorf("expected the entry abandoned after its budget, %d files remain", n)
	}
	if got := svc.spool.droppedAttempts.Load(); got != 1 {
		t.Errorf("expected the drop counted as attempts-exhausted, got %d", got)
	}

	var notified bool
	for _, msg := range notifier.all() {
		if msg == "AgentPostbackAbandoned:id:undeliverable" {
			notified = true
		}
	}
	if !notified {
		t.Errorf("expected an abandonment notification, got %v", notifier.all())
	}
}

// TestAttemptPostback_ClassifiesOutcomes pins the mapping the spool flush relies
// on. Collapsing "the engine rejected this request" back into "the engine is
// unreachable" is precisely the sc-106112 defect, so the split is asserted
// directly rather than only through its consequences.
func TestAttemptPostback_ClassifiesOutcomes(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		header  string // Retry-After, when set
		want    deliveryOutcome
		wantErr bool
	}{
		{
			name:   "accepted",
			status: http.StatusOK,
			body:   "",
			want:   deliveryDone,
		},
		{
			name:    "429 is a throttle whatever its body, never a refusal of the result",
			status:  http.StatusTooManyRequests,
			body:    `{"error":"rate limited"}`,
			want:    deliveryThrottled,
			wantErr: true,
		},
		{
			name:    "503 with Retry-After is a throttle",
			status:  http.StatusServiceUnavailable,
			body:    `{"error":"busy"}`,
			header:  "30",
			want:    deliveryThrottled,
			wantErr: true,
		},
		{
			name:    "503 without Retry-After is still a rejection of this entry",
			status:  http.StatusServiceUnavailable,
			body:    `{"error":"busy"}`,
			want:    deliveryRetryEntry,
			wantErr: true,
		},
		{
			name:    "Retry-After on a 500 is ignored: only 429 and 503 are throttles",
			status:  http.StatusInternalServerError,
			body:    `{"error":"boom"}`,
			header:  "30",
			want:    deliveryRetryEntry,
			wantErr: true,
		},
		{
			name:   "already fulfilled",
			status: http.StatusBadRequest,
			body:   `{"error":"request already fulfilled"}`,
			want:   deliveryDone,
		},
		{
			name:    "permanent rejection",
			status:  http.StatusBadRequest,
			body:    `{"error":"malformed result"}`,
			want:    deliveryDone,
			wantErr: true,
		},
		{
			name:    "server error is the engine refusing this entry, not an outage",
			status:  http.StatusInternalServerError,
			body:    `{"error":"boom"}`,
			want:    deliveryRetryEntry,
			wantErr: true,
		},
		{
			name:    "unparseable body is treated as a rejection of this entry",
			status:  http.StatusTeapot,
			body:    `<html>not json</html>`,
			want:    deliveryRetryEntry,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler := func(w http.ResponseWriter, r *http.Request) {
				if tc.header != "" {
					w.Header().Set("Retry-After", tc.header)
				}
				w.WriteHeader(tc.status)
				if tc.body != "" {
					_, _ = w.Write([]byte(tc.body))
				}
			}
			srv := httptest.NewServer(http.HandlerFunc(handler))
			defer srv.Close()

			svc := newProcessMessageSvc(&mockExecutor{}, &http.Client{
				Transport: &schemeRewriteTransport{scheme: "http"},
			})
			device := deviceWithEngine(srv.Listener.Addr().String())
			msg := &interpreter.Message{PostId: "id:1"}

			got, err := svc.attemptPostback(
				context.Background(), msg, device, []byte(`{}`), hclog.NewNullLogger(), 1,
			)
			if got != tc.want {
				t.Errorf("outcome = %v, want %v", got, tc.want)
			}
			if tc.wantErr && err == nil {
				t.Error("expected an error describing the failure, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected no error, got %v", err)
			}
		})
	}
}

// A transport failure — nothing listening at all — is the one case that really
// does mean the engine is unreachable, and it must stay distinguishable from a
// rejection so the flush still stops early.
func TestAttemptPostback_TransportFailureIsUnreachable(t *testing.T) {
	// Bind and immediately close so the port is almost certainly refusing.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.Listener.Addr().String()
	srv.Close()

	svc := newProcessMessageSvc(&mockExecutor{}, &http.Client{
		Timeout:   2 * time.Second,
		Transport: &schemeRewriteTransport{scheme: "http"},
	})
	device := deviceWithEngine(addr)
	msg := &interpreter.Message{PostId: "id:1"}

	got, err := svc.attemptPostback(
		context.Background(), msg, device, []byte(`{}`), hclog.NewNullLogger(), 1,
	)
	if got != deliveryUnreachable {
		t.Errorf("outcome = %v, want deliveryUnreachable", got)
	}
	if err == nil {
		t.Error("expected the transport error to be returned")
	}
}

// The flush must still stop early on a genuine outage: with nothing listening,
// only the first entry is attempted and every entry stays spooled.
func TestFlushPostbackSpool_StopsEarlyWhenEngineUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	addr := srv.Listener.Addr().String()
	srv.Close()

	svc := newProcessMessageSvc(&mockExecutor{result: []byte(`{}`)}, &http.Client{
		Timeout:   2 * time.Second,
		Transport: &schemeRewriteTransport{scheme: "http"},
	})
	svc.spool = newPostbackSpool(
		t.TempDir(), 10, time.Hour, defaultSpoolMaxAttempts, hclog.NewNullLogger(),
	)

	for _, id := range []string{"a", "b", "c"} {
		if err := svc.spool.enqueue(spoolEntry{
			PostId:    id,
			Result:    []byte(`{}`),
			CreatedAt: time.Now(),
		}); err != nil {
			t.Fatalf("enqueue %s: %v", id, err)
		}
		time.Sleep(time.Millisecond) // sleep-ok: distinct CreatedAt/filenames; ordering, not timing
	}

	device := deviceWithEngine(addr)
	svc.flushPostbackSpool(context.Background(), device, hclog.NewNullLogger(), nil)

	if n := countSpoolFiles(t, svc.spool.dir); n != 3 {
		t.Errorf("expected every entry retained through the outage, %d remain", n)
	}
	if got := svc.spool.droppedTotal.Load(); got != 0 {
		t.Errorf("an outage must drop nothing, got %d drops", got)
	}
}

// throttledServer answers each postback with the scripted status and
// Retry-After, then 200, stamping when every request arrived.
type throttledServer struct {
	*httptest.Server
	mu       sync.Mutex
	arrivals []time.Time
	answers  []throttledAnswer
}

type throttledAnswer struct {
	status     int
	retryAfter string
}

func newThrottledServer(t *testing.T, answers ...throttledAnswer) *throttledServer {
	t.Helper()
	s := &throttledServer{answers: answers}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		i := len(s.arrivals)
		s.arrivals = append(s.arrivals, time.Now())
		s.mu.Unlock()
		if i < len(s.answers) {
			if s.answers[i].retryAfter != "" {
				w.Header().Set("Retry-After", s.answers[i].retryAfter)
			}
			w.WriteHeader(s.answers[i].status)
			_, _ = w.Write([]byte(`{"error":"scripted"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *throttledServer) seen() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Time(nil), s.arrivals...)
}

// The wait a throttle carries is the header's value, floored at the backoff
// the attempt would have waited anyway and capped at the backoff ceiling; a
// 429 without the header waits the backoff itself (sc-132997).
func TestAttemptPostback_ThrottleWaitIsBounded(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		header     string
		wantMin    time.Duration
		wantMax    time.Duration
		fromHeader bool
	}{
		{
			name:       "header inside the bounds is honoured exactly",
			status:     http.StatusTooManyRequests,
			header:     "20",
			wantMin:    20 * time.Second,
			wantMax:    20 * time.Second,
			fromHeader: true,
		},
		{
			name:       "header above the ceiling is capped at it",
			status:     http.StatusTooManyRequests,
			header:     "100000",
			wantMin:    postbackMaxRetryBackoff,
			wantMax:    postbackMaxRetryBackoff,
			fromHeader: true,
		},
		{
			name:   "header of zero is floored at the backoff",
			status: http.StatusTooManyRequests,
			header: "0",
			// The first retry slot: the base (1 ms in these tests) with jitter.
			wantMin:    time.Millisecond * 3 / 4,
			wantMax:    time.Millisecond * 5 / 4,
			fromHeader: true,
		},
		{
			name:       "http date is honoured",
			status:     http.StatusServiceUnavailable,
			header:     time.Now().Add(45 * time.Second).UTC().Format(http.TimeFormat),
			wantMin:    43 * time.Second,
			wantMax:    45 * time.Second,
			fromHeader: true,
		},
		{
			name:       "429 without a header waits the backoff",
			status:     http.StatusTooManyRequests,
			wantMin:    time.Millisecond * 3 / 4,
			wantMax:    time.Millisecond * 5 / 4,
			fromHeader: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newThrottledServer(t, throttledAnswer{status: tc.status, retryAfter: tc.header})
			svc := newProcessMessageSvc(&mockExecutor{}, &http.Client{
				Transport: &schemeRewriteTransport{scheme: "http"},
			})
			device := deviceWithEngine(srv.Listener.Addr().String())

			got, err := svc.attemptPostback(
				context.Background(), &interpreter.Message{PostId: "id:1"}, device,
				[]byte(`{}`), hclog.NewNullLogger(), 1,
			)
			if got != deliveryThrottled {
				t.Fatalf("outcome = %v, want deliveryThrottled", got)
			}
			var throttled *engineThrottled
			if !errors.As(err, &throttled) {
				t.Fatalf("error %v does not carry the throttle", err)
			}
			if throttled.retryAfter < tc.wantMin || throttled.retryAfter > tc.wantMax {
				t.Errorf(
					"retryAfter = %s, want within [%s, %s]",
					throttled.retryAfter,
					tc.wantMin,
					tc.wantMax,
				)
			}
			if throttled.fromHeader != tc.fromHeader {
				t.Errorf("fromHeader = %v, want %v", throttled.fromHeader, tc.fromHeader)
			}
			if throttled.status != tc.status {
				t.Errorf("status = %d, want %d", throttled.status, tc.status)
			}
		})
	}
}

// A 429 with Retry-After holds the in-line retry for the time the engine
// asked, not the agent's own millisecond schedule: the second attempt must not
// land inside the window, and the result is delivered by it (sc-132997).
func TestProcessMessage_PostbackHonoursRetryAfterInLine(t *testing.T) {
	srv := newThrottledServer(
		t,
		throttledAnswer{status: http.StatusTooManyRequests, retryAfter: "1"},
	)
	svc := newProcessMessageSvc(&mockExecutor{result: []byte(`{}`)}, &http.Client{
		Transport: &schemeRewriteTransport{scheme: "http"},
	})
	device := deviceWithEngine(srv.Listener.Addr().String())

	svc.processMessage(
		postbackPayload("echo hi", "id:throttled"),
		context.Background(), device, hclog.NewNullLogger(), &mockNotifierWrapper{},
	)

	seen := srv.seen()
	if len(seen) != 2 {
		t.Fatalf("expected the throttled attempt and one delivery, saw %d requests", len(seen))
	}
	if gap := seen[1].Sub(seen[0]); gap < time.Second {
		t.Errorf(
			"second attempt landed %s after the first, inside the 1 s the engine asked for",
			gap,
		)
	}
}

// A throttled spool flush waits the engine's time inside the same cycle and
// runs again, so the result is delivered without waiting for the next
// connection; the throttle is not counted against the entry (sc-132997).
func TestFlushPostbackSpool_ThrottledFlushResumesAfterTheWait(t *testing.T) {
	// A Retry-After of zero floors at the test backoff (1 ms), so the re-run
	// is near-immediate and the test is not timing-sensitive.
	srv := newThrottledServer(
		t,
		throttledAnswer{status: http.StatusTooManyRequests, retryAfter: "0"},
	)
	svc := newProcessMessageSvc(&mockExecutor{result: []byte(`{}`)}, &http.Client{
		Transport: &schemeRewriteTransport{scheme: "http"},
	})
	svc.spool = newPostbackSpool(
		t.TempDir(), 10, time.Hour, defaultSpoolMaxAttempts, hclog.NewNullLogger(),
	)
	if err := svc.spool.enqueue(spoolEntry{
		PostId: "id:throttled", Result: []byte(`{}`), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	device := deviceWithEngine(srv.Listener.Addr().String())
	svc.flushPostbackSpool(context.Background(), device, hclog.NewNullLogger(), nil)

	if n := len(srv.seen()); n != 2 {
		t.Errorf("expected the throttled attempt and one delivery within the flush, saw %d", n)
	}
	if n := countSpoolFiles(t, svc.spool.dir); n != 0 {
		t.Errorf("expected the entry delivered, %d remain", n)
	}
	if got := svc.spool.droppedTotal.Load(); got != 0 {
		t.Errorf("a throttle must drop nothing, got %d drops", got)
	}
	if h := svc.spool.health(); h.NextAttemptAt != "" {
		t.Errorf(
			"no pause should remain once the flush ran to the end, got next_attempt_at %q",
			h.NextAttemptAt,
		)
	}
}

// The cycle ending during the wait ends the flush: the entry stays spooled
// for the next cycle rather than the wait outliving its context.
func TestFlushPostbackSpool_ThrottledWaitEndsWithTheCycle(t *testing.T) {
	srv := newThrottledServer(
		t,
		throttledAnswer{status: http.StatusTooManyRequests, retryAfter: "60"},
	)
	svc := newProcessMessageSvc(&mockExecutor{result: []byte(`{}`)}, &http.Client{
		Transport: &schemeRewriteTransport{scheme: "http"},
	})
	svc.spool = newPostbackSpool(
		t.TempDir(), 10, time.Hour, defaultSpoolMaxAttempts, hclog.NewNullLogger(),
	)
	if err := svc.spool.enqueue(spoolEntry{
		PostId: "id:throttled", Result: []byte(`{}`), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.flushPostbackSpool(
			ctx,
			deviceWithEngine(srv.Listener.Addr().String()),
			hclog.NewNullLogger(),
			nil,
		)
	}()

	// The throttled attempt lands first; then the flush is waiting.
	deadline := time.Now().Add(5 * time.Second)
	for len(srv.seen()) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond) // sleep-ok: poll interval
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the flush kept waiting after its cycle ended")
	}

	if n := len(srv.seen()); n != 1 {
		t.Errorf("expected only the throttled attempt, saw %d requests", n)
	}
	if n := countSpoolFiles(t, svc.spool.dir); n != 1 {
		t.Errorf("expected the entry kept for the next cycle, %d remain", n)
	}
	if h := svc.spool.health(); h.NextAttemptAt == "" {
		t.Error("expected the pause reported in the health object while it is in force")
	}
}

// A budget that ends on a throttle spools the result with the engine's time
// on it, and the spool is paused at once, so the flush on a cycle that starts
// seconds later does not knock inside the window (sc-132997).
func TestProcessMessage_ThrottledExhaustionSpoolsWithTheEnginesTime(t *testing.T) {
	srv := newThrottledServer(
		t,
		throttledAnswer{status: http.StatusTooManyRequests, retryAfter: "30"},
	)
	svc := newProcessMessageSvc(&mockExecutor{result: []byte(`{}`)}, &http.Client{
		Transport: &schemeRewriteTransport{scheme: "http"},
	})
	svc.PostbackMaxAttempts = 1 // the one attempt is the throttled one
	svc.spool = newPostbackSpool(
		t.TempDir(), 10, time.Hour, defaultSpoolMaxAttempts, hclog.NewNullLogger(),
	)
	device := deviceWithEngine(srv.Listener.Addr().String())

	before := time.Now()
	svc.processMessage(
		postbackPayload("echo hi", "id:throttled"),
		context.Background(), device, hclog.NewNullLogger(), &mockNotifierWrapper{},
	)

	entries := readSpoolEntries(t, svc.spool.dir)
	if len(entries) != 1 {
		t.Fatalf("expected the result spooled, got %d entries", len(entries))
	}
	want := before.Add(30 * time.Second)
	if entries[0].NextAttemptAt.Before(want) ||
		entries[0].NextAttemptAt.After(want.Add(5*time.Second)) {
		t.Errorf("next_attempt_at = %s, want about %s", entries[0].NextAttemptAt, want)
	}
	if h := svc.spool.health(); h.NextAttemptAt == "" {
		t.Error("the pause is not reported in the health object right after the spooling")
	}

	// The flush that follows attempts nothing before the time.
	svc.flushPostbackSpool(
		contextWithTimeout(t, 200*time.Millisecond),
		device,
		hclog.NewNullLogger(),
		nil,
	)
	if n := len(srv.seen()); n != 1 {
		t.Errorf("the flush attempted the entry inside the window: %d requests", n)
	}
}

func contextWithTimeout(t *testing.T, d time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	t.Cleanup(cancel)
	return ctx
}
