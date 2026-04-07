package main

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/interpreter"
	"github.com/hashicorp/go-hclog"
)

// mockNotifierWrapper implements plugins.NotifierWrapper for tests.
type mockNotifierWrapper struct{}

func (m *mockNotifierWrapper) Kill()               {}
func (m *mockNotifierWrapper) Plugins() []string   { return nil }
func (m *mockNotifierWrapper) Notify(string) error { return nil }

// countingExecutor counts Execute calls and optionally blocks until released.
type countingExecutor struct {
	count  atomic.Int32
	block  chan struct{} // if non-nil, Execute blocks until this is closed
	result []byte
}

func (e *countingExecutor) AlwaysPostback() bool { return false }

func (e *countingExecutor) Execute(
	_ context.Context,
	_ *interpreter.Message,
	_ agent.Device,
	_ hclog.Logger,
	_ agent.SystemInfoProvider,
	_ agent.DomainInfoProvider,
) []byte {
	e.count.Add(1)
	if e.block != nil {
		<-e.block
	}
	return e.result
}

// validPayload builds a minimal valid message JSON with no post_id so no
// postback is attempted.
func validPayload(commands string) []byte {
	type msg struct {
		Commands string `json:"commands"`
	}
	b, _ := json.Marshal(msg{Commands: commands})
	return b
}

func newTestSvc(exec interpreter.Executor) *serviceContext {
	return &serviceContext{
		Executor: exec,
		Sys:      &mockSystemInfoProvider{hostname: "host", hostPlatform: "linux"},
		Domain:   &mockDomainInfoProvider{},
	}
}

// TestMessageQueue_WorkersProcessAllMessages verifies that all enqueued
// messages are eventually processed.
func TestMessageQueue_WorkersProcessAllMessages(t *testing.T) {
	exec := &countingExecutor{}
	svc := newTestSvc(exec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	device := agent.Device{}

	msgQueue := make(chan []byte, messageQueueSize)

	var wg sync.WaitGroup
	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case payload, ok := <-msgQueue:
					if !ok {
						return
					}
					svc.processMessage(payload, ctx, device, logger, notifier)
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

	// Drain: wait until all messages have been processed.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if int(exec.count.Load()) >= total {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	close(msgQueue)
	wg.Wait()

	if got := exec.count.Load(); int(got) != total {
		t.Errorf("expected %d messages processed, got %d", total, got)
	}
}

// TestMessageQueue_QueueFullDropsMessage verifies that when the queue is full
// the callback drops the message rather than blocking.
func TestMessageQueue_QueueFullDropsMessage(t *testing.T) {
	// workerBlocked is incremented each time a worker enters Execute.
	workerEntered := make(chan struct{}, workerCount)
	block := make(chan struct{})

	exec := &blockingConcurrencyExecutor{
		block:   block,
		onEnter: func() { workerEntered <- struct{}{} },
		onExit:  func() {},
	}
	svc := newTestSvc(exec)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	device := agent.Device{}

	msgQueue := make(chan []byte, messageQueueSize)

	// Start workers.
	for range workerCount {
		go func() {
			for {
				select {
				case payload, ok := <-msgQueue:
					if !ok {
						return
					}
					svc.processMessage(payload, ctx, device, logger, notifier)
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// Send exactly workerCount messages and wait until every worker is blocked
	// inside Execute — at that point no worker can drain the queue further.
	for range workerCount {
		msgQueue <- validPayload("echo block")
	}
	for range workerCount {
		select {
		case <-workerEntered:
		case <-time.After(2 * time.Second):
			t.Fatal("workers did not enter Execute in time")
		}
	}

	// Now fill the queue to its full capacity.
	for range messageQueueSize {
		msgQueue <- validPayload("echo fill")
	}

	// Queue is full. This select must take the default (drop) branch.
	dropped := false
	select {
	case msgQueue <- validPayload("echo overflow"):
	default:
		dropped = true
	}

	if !dropped {
		t.Error("expected message to be dropped when queue is full, but it was enqueued")
	}

	close(block) // unblock workers so goroutines can exit
}

// TestMessageQueue_WorkersCancelOnContextDone verifies that all workers exit
// cleanly when the context is cancelled.
func TestMessageQueue_WorkersCancelOnContextDone(t *testing.T) {
	exec := &countingExecutor{}
	svc := newTestSvc(exec)

	ctx, cancel := context.WithCancel(context.Background())

	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	device := agent.Device{}

	msgQueue := make(chan []byte, messageQueueSize)

	var wg sync.WaitGroup
	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case payload, ok := <-msgQueue:
					if !ok {
						return
					}
					svc.processMessage(payload, ctx, device, logger, notifier)
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	cancel()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("workers did not exit after context cancellation")
	}
}

// TestMessageQueue_ConcurrencyBoundedByWorkerCount verifies that at most
// workerCount messages are being processed simultaneously.
func TestMessageQueue_ConcurrencyBoundedByWorkerCount(t *testing.T) {
	block := make(chan struct{})

	var (
		mu      sync.Mutex
		current int
		peak    int
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	device := agent.Device{}

	msgQueue := make(chan []byte, messageQueueSize)

	// Use a custom svc that tracks concurrency via a wrapped Executor.
	concurrentExec := &blockingConcurrencyExecutor{
		block: block,
		onEnter: func() {
			mu.Lock()
			current++
			if current > peak {
				peak = current
			}
			mu.Unlock()
		},
		onExit: func() {
			mu.Lock()
			current--
			mu.Unlock()
		},
	}
	svc := newTestSvc(concurrentExec)

	var wg sync.WaitGroup
	for range workerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case payload, ok := <-msgQueue:
					if !ok {
						return
					}
					svc.processMessage(payload, ctx, device, logger, notifier)
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	// Send exactly workerCount messages so all workers are busy.
	for range workerCount {
		msgQueue <- validPayload("echo concurrent")
	}

	// Wait until all workers are inside Execute.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		c := current
		mu.Unlock()
		if c == workerCount {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	close(block) // unblock all workers

	close(msgQueue)
	wg.Wait()

	if peak > workerCount {
		t.Errorf("peak concurrency %d exceeded workerCount %d", peak, workerCount)
	}
	if peak == 0 {
		t.Error("no messages were processed")
	}
}

// blockingConcurrencyExecutor blocks until its block channel is closed,
// calling onEnter/onExit around the block to track concurrency.
type blockingConcurrencyExecutor struct {
	block   chan struct{}
	onEnter func()
	onExit  func()
}

func (e *blockingConcurrencyExecutor) AlwaysPostback() bool { return false }

func (e *blockingConcurrencyExecutor) Execute(
	_ context.Context,
	_ *interpreter.Message,
	_ agent.Device,
	_ hclog.Logger,
	_ agent.SystemInfoProvider,
	_ agent.DomainInfoProvider,
) []byte {
	e.onEnter()
	<-e.block
	e.onExit()
	return nil
}
