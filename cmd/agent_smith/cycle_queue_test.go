package main

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/hashicorp/go-hclog"
)

// The race sc-119839 closes: producers sending in a tight loop while teardown
// raises draining and closes the queue. Before the lock, a producer that passed
// the non-blocking draining check just before it closed could reach its select
// after the channel was closed and panic. 1 000 iterations under -race in the
// normal suite; each one is microseconds.
func TestCycleQueue_TeardownRaceNeverSendsOnAClosedChannel(t *testing.T) {
	const iterations = 1000
	for i := 0; i < iterations; i++ {
		q := newCycleQueue(1)
		var wg sync.WaitGroup
		var panics atomic.Int32
		var sent atomic.Int64

		// A consumer, so the producers alternate between a free slot and
		// back-pressure rather than sitting blocked.
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range q.ch {
			}
		}()
		for p := 0; p < 2; p++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						panics.Add(1)
					}
				}()
				for q.send(inboundMessage{}) {
					sent.Add(1)
				}
			}()
		}

		q.startDraining()
		q.close()
		wg.Wait()

		if n := panics.Load(); n != 0 {
			t.Fatalf("iteration %d: %d producer(s) panicked sending on the closed queue", i, n)
		}
		// Idempotent teardown: a second close or drain is harmless.
		q.close()
		q.startDraining()
	}
}

// close must not be able to deadlock against a producer blocked on a full
// queue: startDraining (which close performs itself) releases the producer,
// which drops its read lock, and close then takes the write lock.
func TestCycleQueue_CloseReleasesABlockedProducer(t *testing.T) {
	q := newCycleQueue(1)
	q.ch <- inboundMessage{} // full

	result := make(chan bool, 1)
	go func() { result <- q.send(inboundMessage{}) }()

	// close without an explicit startDraining: it has to do that itself.
	q.close()

	if got := <-result; got {
		t.Fatal("a send released by teardown reported success")
	}
	if _, ok := <-q.ch; !ok {
		t.Fatal("the buffered item was lost at close")
	}
	if _, ok := <-q.ch; ok {
		t.Fatal("queue still open after close")
	}
}

func TestCycleQueue_SendAfterCloseIsRefusedNotPanicking(t *testing.T) {
	q := newCycleQueue(4)
	q.close()
	if q.send(inboundMessage{}) {
		t.Fatal("send after close reported success")
	}
}

// The same race driven through the service's enqueueMessage under SafeGo, the
// way the replay goroutine runs in production: utils.Recover still guards the
// path, but the log line it would write for a recovered panic must be absent.
func TestEnqueueMessage_TeardownRaceLeavesNoRecoveredPanicInTheLog(t *testing.T) {
	var buf syncBuffer
	logger := hclog.New(&hclog.LoggerOptions{Output: &buf, Level: hclog.Trace})
	svc := &serviceContext{}
	notifier := &recordingNotifierWrapper{}

	for i := 0; i < 300; i++ {
		q := newCycleQueue(1)
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range q.ch {
			}
		}()
		wg.Add(1)
		utils.SafeGo(logger, func() {
			defer wg.Done()
			// Journaled items only: an unjournaled one turned away at teardown is
			// the loud, counted drop path, which is not what this test is about.
			item := inboundMessage{Key: "k", Payload: []byte("{}")}
			for svc.enqueueMessage(item, q, 1, logger, notifier) {
			}
		}, "scope", "command_journal_replay")
		q.startDraining()
		q.close()
		wg.Wait()
	}

	if out := buf.String(); strings.Contains(out, "Recovered from panic") {
		t.Fatalf("a send on the closed queue was recovered rather than prevented:\n%s", out)
	}
	if n := svc.droppedMessages.Load(); n != 0 {
		t.Errorf("journaled items turned away at teardown were counted as dropped: %d", n)
	}
}
