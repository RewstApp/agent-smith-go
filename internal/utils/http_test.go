package utils

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/RewstApp/agent-smith-go/internal/version"
)

func TestNewRequestWithContext_SetsVersionHeader(t *testing.T) {
	req, err := NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"https://example.com",
		nil,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expected := version.Version[1:]
	got := req.Header.Get("x-rewst-agent-smith-version")
	if got != expected {
		t.Errorf("expected header %q, got %q", expected, got)
	}
}

func TestNewRequestWithContext_SetsMethodAndURL(t *testing.T) {
	req, err := NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"https://example.com/path",
		nil,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if req.Method != http.MethodPost {
		t.Errorf("expected method POST, got %s", req.Method)
	}

	if req.URL.String() != "https://example.com/path" {
		t.Errorf("expected URL 'https://example.com/path', got %s", req.URL.String())
	}
}

func TestNewRequestWithContext_WithBody(t *testing.T) {
	body := bytes.NewBufferString(`{"key":"value"}`)
	req, err := NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"https://example.com",
		body,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if req.Body == nil {
		t.Error("expected non-nil body")
	}
}

func TestNewRequestWithContext_InvalidURL(t *testing.T) {
	_, err := NewRequestWithContext(context.Background(), http.MethodGet, "://invalid-url", nil)
	if err == nil {
		t.Error("expected error for invalid URL, got nil")
	}
}

func TestNewRequestWithContext_PropagatesContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req, err := NewRequestWithContext(ctx, http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("expected no error creating request, got %v", err)
	}

	if req.Context().Err() == nil {
		t.Error("expected cancelled context to propagate to request")
	}
}

func TestNewRequest_SetsVersionHeader(t *testing.T) {
	req, err := NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expected := version.Version[1:]
	got := req.Header.Get("x-rewst-agent-smith-version")
	if got != expected {
		t.Errorf("expected header %q, got %q", expected, got)
	}
}

func TestNewRequest_UsesBackgroundContext(t *testing.T) {
	req, err := NewRequest(http.MethodGet, "https://example.com", nil)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if req.Context() == nil {
		t.Error("expected non-nil context")
	}

	if req.Context().Err() != nil {
		t.Errorf("expected non-cancelled context, got %v", req.Context().Err())
	}
}
