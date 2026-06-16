package agent

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/utils"
)

// TestRunUpdate_RecoversPanic verifies that a panic on the update path is
// recovered, logged at Error level with a stack trace, and surfaced as an error
// so the normal failure-handling flow continues.
func TestRunUpdate_RecoversPanic(t *testing.T) {
	buf := bytes.Buffer{}
	logger := utils.ConfigureLogger("test", &buf, utils.Error)
	mock := &mockUpdater{
		runFn: func(_ context.Context) error {
			panic("update tick boom")
		},
	}

	runner := NewAutoUpdateRunner(logger, mock, time.Hour, 3, time.Millisecond)

	err := runner.runUpdate()
	if err == nil {
		t.Fatal("expected error from recovered panic, got nil")
	}

	output := buf.String()
	if !strings.Contains(output, "Recovered from panic") {
		t.Errorf("expected recovery log, got %q", output)
	}
	if !strings.Contains(output, "update tick boom") {
		t.Errorf("expected recovered value in log, got %q", output)
	}
	if !strings.Contains(output, "stack=") {
		t.Errorf("expected stack trace in log, got %q", output)
	}
	if !strings.Contains(strings.ToLower(output), "[error]") {
		t.Errorf("expected error-level log, got %q", output)
	}
}

// TestAutoUpdateRunner_SurvivesPanickingTick verifies that when an update tick
// panics, the runner does not crash and continues running on its schedule.
func TestAutoUpdateRunner_SurvivesPanickingTick(t *testing.T) {
	logger := utils.ConfigureLogger("test", &bytes.Buffer{}, utils.Off)

	var calls atomic.Int32
	mock := &mockUpdater{
		runFn: func(_ context.Context) error {
			// Panic on the first tick only; subsequent ticks succeed.
			if calls.Add(1) == 1 {
				panic("tick boom")
			}
			return nil
		},
	}

	// Short interval and backoff so the panic tick, its retry, and at least one
	// further successful tick all happen quickly.
	runner := NewAutoUpdateRunner(logger, mock, 10*time.Millisecond, 3, time.Millisecond)
	runner.Start()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if calls.Load() >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	runner.Stop()

	if calls.Load() < 2 {
		t.Errorf("expected runner to continue ticking after a panic, got %d calls", calls.Load())
	}
}
