package interpreter

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestErrorResultBytes(t *testing.T) {
	err := errors.New("test error")
	result := errorResultBytes(err)

	var out errorResult
	json.Unmarshal(result, &out)
	if out.Error != "test error" {
		t.Errorf("expected 'test error', got %s", out.Error)
	}
}

func TestResultBytes(t *testing.T) {
	b := resultBytes("some error", "some output")

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
