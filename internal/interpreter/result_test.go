package interpreter

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/hashicorp/go-hclog"
)

// assertCompactJSON fails if the bytes contain indentation whitespace
// (newlines or tabs), ensuring postbacks are serialized compactly.
func assertCompactJSON(t *testing.T, b []byte) {
	t.Helper()
	if bytes.ContainsAny(b, "\n\t") {
		t.Errorf("expected compact JSON without indentation, got %s", b)
	}
}

func TestErrorResultBytes(t *testing.T) {
	logger := hclog.NewNullLogger()
	err := errors.New("test error")
	b := errorResultBytes(logger, err)

	if b == nil {
		t.Fatal("expected non-nil bytes")
	}

	assertCompactJSON(t, b)

	var out errorResult
	if err = json.Unmarshal(b, &out); err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if out.Error != "test error" {
		t.Errorf("expected 'test error', got %s", out.Error)
	}
}

func TestResultBytes(t *testing.T) {
	logger := hclog.NewNullLogger()
	b := resultBytes(logger, "some error", "some output", outputTruncation{})

	if b == nil {
		t.Fatal("expected non-nil bytes")
	}

	assertCompactJSON(t, b)

	var out result
	err := json.Unmarshal(b, &out)
	if err != nil {
		t.Fatalf("expected valid JSON, got %v", err)
	}

	if out.Error != "some error" {
		t.Errorf("expected 'some error', got %s", out.Error)
	}

	if out.Output != "some output" {
		t.Errorf("expected 'some output', got %s", out.Output)
	}
}

func TestErrorResultBytesNeverNil(t *testing.T) {
	logger := hclog.NewNullLogger()
	b := errorResultBytes(logger, errors.New("any error"))
	if len(b) == 0 {
		t.Fatal("expected non-empty bytes")
	}
	if !json.Valid(b) {
		t.Errorf("expected valid JSON, got %s", b)
	}
}

func TestResultBytesNeverNil(t *testing.T) {
	logger := hclog.NewNullLogger()
	b := resultBytes(logger, "", "", outputTruncation{})
	if len(b) == 0 {
		t.Fatal("expected non-empty bytes")
	}
	if !json.Valid(b) {
		t.Errorf("expected valid JSON, got %s", b)
	}
}

// TestResultBytesOmitsTruncationWhenComplete pins the wire format for output that
// fit under the ceiling: no truncation keys at all, so a complete postback is
// byte-identical to what previous releases sent.
func TestResultBytesOmitsTruncationWhenComplete(t *testing.T) {
	logger := hclog.NewNullLogger()
	b := resultBytes(logger, "some error", "some output", outputTruncation{})

	if got, want := string(b), `{"error":"some error","output":"some output"}`; got != want {
		t.Errorf("resultBytes() = %s, want %s", got, want)
	}
}

func TestResultBytesCarriesTruncationSignal(t *testing.T) {
	logger := hclog.NewNullLogger()
	trunc := outputTruncation{Truncated: true, Produced: 2_000_000, Kept: 1024}
	b := resultBytes(logger, "some error", "some output", trunc)

	assertCompactJSON(t, b)

	var out result
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("expected valid JSON, got %v", err)
	}

	if !out.Truncated {
		t.Errorf("expected truncated=true, got %s", b)
	}
	if out.OutputBytesProduced != 2_000_000 {
		t.Errorf("output_bytes_produced = %d, want 2000000", out.OutputBytesProduced)
	}
	if out.OutputBytesKept != 1024 {
		t.Errorf("output_bytes_kept = %d, want 1024", out.OutputBytesKept)
	}
	// Truncation must never swallow the output that was captured.
	if out.Output != "some output" {
		t.Errorf("output = %q, want %q", out.Output, "some output")
	}
	if out.Error != "some error" {
		t.Errorf("error = %q, want %q", out.Error, "some error")
	}
	if out.TimedOut {
		t.Errorf("timed_out set on a result that did not time out: %s", b)
	}
}

// TestTimeoutResultBytesComposesWithTruncation covers a command that was both
// verbose and hung: the workflow must see both flags, not one masking the other.
func TestTimeoutResultBytesComposesWithTruncation(t *testing.T) {
	logger := hclog.NewNullLogger()
	trunc := outputTruncation{Truncated: true, Produced: 5000, Kept: 100}
	b := timeoutResultBytes(logger, "command timed out after 1s", "partial", trunc)

	assertCompactJSON(t, b)

	var out result
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("expected valid JSON, got %v", err)
	}

	if !out.TimedOut {
		t.Errorf("expected timed_out=true, got %s", b)
	}
	if !out.Truncated {
		t.Errorf("expected truncated=true, got %s", b)
	}
	if out.OutputBytesProduced != 5000 || out.OutputBytesKept != 100 {
		t.Errorf(
			"byte counts = %d/%d, want 5000/100",
			out.OutputBytesProduced,
			out.OutputBytesKept,
		)
	}
	if out.Output != "partial" {
		t.Errorf("output = %q, want %q", out.Output, "partial")
	}
}

func TestTimeoutResultBytesOmitsTruncationWhenComplete(t *testing.T) {
	logger := hclog.NewNullLogger()
	b := timeoutResultBytes(logger, "command timed out after 1s", "partial", outputTruncation{})

	var out result
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("expected valid JSON, got %v", err)
	}

	if !out.TimedOut {
		t.Errorf("expected timed_out=true, got %s", b)
	}
	if out.Truncated {
		t.Errorf("truncated set on a timeout whose output fit the ceiling: %s", b)
	}
	if bytes.Contains(b, []byte("output_bytes")) {
		t.Errorf("byte counts emitted for untruncated output: %s", b)
	}
}
