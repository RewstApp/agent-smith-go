package main

import (
	"context"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/hashicorp/go-hclog"
)

// While an update is pending, a journaled command the worker dequeues is left
// for the restarted agent: not executed, still pending and not started in the
// journal, and no longer owned so a later cycle of this process would replay
// it if the restart never came (sc-119836).
func TestStartWorkers_HeldJournaledCommandIsParkedForTheRestart(t *testing.T) {
	exec := &countingExecutor{result: []byte(`{"error":"","output":"ok"}`)}
	svc := svcWithJournal(t, exec)
	svc.HoldNewCommands(time.Hour)

	payload := validPayload("echo held")
	key := journalKey(payload)
	if _, err := svc.journal.put(key, payload); err != nil {
		t.Fatal(err)
	}
	svc.owned.Store(key, struct{}{})

	queue := make(chan inboundMessage, 1)
	queue <- inboundMessage{Payload: payload, Key: key}
	close(queue)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.startWorkers(
		ctx,
		queue,
		1,
		agent.Device{},
		hclog.NewNullLogger(),
		&recordingNotifierWrapper{},
	)
	svc.workers.Wait()

	if exec.count.Load() != 0 {
		t.Fatalf("executed %d commands while held, want 0", exec.count.Load())
	}
	entry, err := svc.journal.readLocked(svc.journal.pendingPath(key))
	if err != nil {
		t.Fatalf("entry no longer pending: %v", err)
	}
	if !entry.StartedAt.IsZero() {
		t.Error(
			"a parked command was marked started; the next process would report it interrupted instead of running it",
		)
	}
	if _, owned := svc.owned.Load(key); owned {
		t.Error("ownership not released; a later cycle of this process could not replay it")
	}
	if fresh, _ := svc.snapshotJournal(hclog.NewNullLogger()); len(fresh) != 1 ||
		fresh[0].Key != key {
		t.Errorf("snapshot = %v, want the parked entry", keysOf(fresh))
	}
}

// A command the journal could not record cannot be parked without losing it,
// so it runs even while the hold is on.
func TestStartWorkers_HeldUnjournaledCommandStillRuns(t *testing.T) {
	exec := &countingExecutor{result: []byte(`{"error":"","output":"ok"}`)}
	svc := newTestSvc(exec)
	svc.HoldNewCommands(time.Hour)

	queue := make(chan inboundMessage, 1)
	queue <- inboundMessage{Payload: validPayload("echo unjournaled")}
	close(queue)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.startWorkers(
		ctx,
		queue,
		1,
		agent.Device{},
		hclog.NewNullLogger(),
		&recordingNotifierWrapper{},
	)
	svc.workers.Wait()

	if exec.count.Load() != 1 {
		t.Fatalf("executed %d, want 1", exec.count.Load())
	}
}

// Executing counts a command from the moment a worker takes it to the moment
// its outcome has settled, which is what the updater polls.
func TestExecuting_CountsCommandsInFlight(t *testing.T) {
	exec := &countingExecutor{
		block:  make(chan struct{}),
		result: []byte(`{"error":"","output":"ok"}`),
	}
	svc := newTestSvc(exec)
	if svc.Executing() != 0 {
		t.Fatalf("idle service reports %d executing", svc.Executing())
	}

	queue := make(chan inboundMessage, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.startWorkers(
		ctx,
		queue,
		1,
		agent.Device{},
		hclog.NewNullLogger(),
		&recordingNotifierWrapper{},
	)
	queue <- inboundMessage{Payload: validPayload("echo slow")}

	waitFor(t, "the command to be counted in flight", func() bool { return svc.Executing() == 1 })
	close(exec.block)
	waitFor(t, "the count to return to zero", func() bool { return svc.Executing() == 0 })
	close(queue)
	svc.workers.Wait()
}

func TestHoldNewCommands_LapsesAndCanBeReleased(t *testing.T) {
	svc := newTestSvc(&countingExecutor{})
	if svc.commandsHeld() {
		t.Fatal("held with no hold set")
	}
	svc.HoldNewCommands(time.Hour)
	if !svc.commandsHeld() {
		t.Fatal("not held after HoldNewCommands")
	}
	svc.ReleaseNewCommands()
	if svc.commandsHeld() {
		t.Fatal("still held after ReleaseNewCommands")
	}
	svc.HoldNewCommands(time.Millisecond)
	waitFor(t, "the hold to lapse", func() bool { return !svc.commandsHeld() })
}
