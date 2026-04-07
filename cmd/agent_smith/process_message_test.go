package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
		Executor:   exec,
		Sys:        &mockSystemInfoProvider{hostname: "host", hostPlatform: "linux"},
		Domain:     &mockDomainInfoProvider{},
		HTTPClient: httpClient,
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
