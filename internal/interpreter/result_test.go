package interpreter

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/hashicorp/go-hclog"
)

func TestErrorResultBytes(t *testing.T) {
	logger := hclog.NewNullLogger()
	err := errors.New("test error")
	b := errorResultBytes(logger, err)

	if b == nil {
		t.Fatal("expected non-nil bytes")
	}

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
	b := resultBytes(logger, "some error", "some output")

	if b == nil {
		t.Fatal("expected non-nil bytes")
	}

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
	b := resultBytes(logger, "", "")
	if len(b) == 0 {
		t.Fatal("expected non-empty bytes")
	}
	if !json.Valid(b) {
		t.Errorf("expected valid JSON, got %s", b)
	}
}
