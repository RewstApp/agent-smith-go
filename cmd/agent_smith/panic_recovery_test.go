package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/interpreter"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/hashicorp/go-hclog"
)

// panicOnceExecutor panics on its first Execute call and counts successful
// (non-panicking) calls thereafter, so a test can assert the worker survived a
// panic and continued processing subsequent messages.
type panicOnceExecutor struct {
	calls   atomic.Int32
	survive atomic.Int32
}

func (e *panicOnceExecutor) AlwaysPostback() bool { return false }

func (e *panicOnceExecutor) Execute(
	_ context.Context,
	_ *interpreter.Message,
	_ agent.Device,
	_ hclog.Logger,
	_ agent.SystemInfoProvider,
	_ agent.DomainInfoProvider,
) []byte {
	if e.calls.Add(1) == 1 {
		panic("simulated handler panic")
	}
	e.survive.Add(1)
	return nil
}

// TestProcessMessageGuarded_RecoversAndLogs verifies a panic while handling a
// single message is recovered (the call returns normally), logged at Error
// level with a stack trace, and never propagates to crash the process.
func TestProcessMessageGuarded_RecoversAndLogs(t *testing.T) {
	exec := &panicOnceExecutor{}
	svc := newTestSvc(exec)

	buf := bytes.Buffer{}
	logger := utils.ConfigureLogger("test", &buf, utils.Error)
	notifier := &mockNotifierWrapper{}
	device := agent.Device{}

	// Must not panic — if recovery were missing, this call would crash the test
	// process (and the agent in production).
	svc.processMessageGuarded(7, validPayload("echo boom"), context.Background(), device, logger, notifier)

	output := buf.String()
	if !strings.Contains(output, "Recovered from panic") {
		t.Errorf("expected recovery log, got %q", output)
	}
	if !strings.Contains(output, "simulated handler panic") {
		t.Errorf("expected recovered value in log, got %q", output)
	}
	if !strings.Contains(output, "stack=") {
		t.Errorf("expected stack trace in log, got %q", output)
	}
	if !strings.Contains(output, "worker=7") {
		t.Errorf("expected worker id in log, got %q", output)
	}
	if !strings.Contains(strings.ToLower(output), "[error]") {
		t.Errorf("expected error-level log, got %q", output)
	}
}

// TestWorkerPool_SurvivesPanickingHandler verifies the worker pool keeps
// processing subsequent messages after a handler panics — i.e. a panicking
// message does not permanently kill a worker. It drives the same guarded
// worker loop used by runCycle.
func TestWorkerPool_SurvivesPanickingHandler(t *testing.T) {
	exec := &panicOnceExecutor{}
	svc := newTestSvc(exec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	device := agent.Device{}

	msgQueue := make(chan []byte, messageQueueSize)

	var wg sync.WaitGroup
	for i := range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case payload, ok := <-msgQueue:
					if !ok {
						return
					}
					svc.processMessageGuarded(i, payload, ctx, device, logger, notifier)
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	const total = 20
	for range total {
		msgQueue <- validPayload("echo hi")
	}

	// All messages must be consumed (the first triggers the panic; the rest are
	// processed normally), proving no worker died permanently.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if int(exec.calls.Load()) >= total {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	close(msgQueue)
	wg.Wait()

	if got := int(exec.calls.Load()); got != total {
		t.Errorf("expected all %d messages handled, got %d", total, got)
	}
	if got := int(exec.survive.Load()); got != total-1 {
		t.Errorf("expected %d messages processed after the panic, got %d", total-1, got)
	}
}
