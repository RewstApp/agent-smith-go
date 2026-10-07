package main

import "sync"

// cycleQueue is the hand-off between a connection cycle's producers - the
// paho subscribe callback (receiveMessage) and the journal replay goroutine
// (replayJournal) - and the service-scoped command workers that drain it.
//
// Its job is to make the cycle's teardown safe against a producer that is
// mid-send. Teardown has to close the channel so the workers know the cycle's
// feed has ended, and a send on a closed channel panics regardless of which
// other select case is ready. The non-blocking check of draining in send is the
// fast path (sc-118039), but it only narrows the window: a producer that
// passed the check just before draining closed can reach its select after the
// channel is closed. Until sc-119839 that panic was survived only because
// utils.SafeGo recovered it - a logged panic in the receive path, and a replay
// pass abandoned mid-loop.
//
// The design is a reader/writer lock around the channel's lifetime: every send
// holds mu for reading from the moment it decides the queue is open until the
// send has either completed or been released by draining; close takes mu for
// writing before closing the channel, so it cannot proceed while any send is
// in flight. The lock order is what keeps this deadlock-free: startDraining
// closes draining without the lock and is always called first (close calls it
// itself, defensively), so a producer blocked on a full queue is released and
// drops its read lock before close waits for the write lock. The channel is
// therefore never closed while a goroutine holds a reference it is about to
// send on, and send never has to recover from anything.
type cycleQueue struct {
	ch       chan inboundMessage
	draining chan struct{}

	mu        sync.RWMutex
	closed    bool
	drainOnce sync.Once
}

func newCycleQueue(size int) *cycleQueue {
	return &cycleQueue{
		ch:       make(chan inboundMessage, size),
		draining: make(chan struct{}),
	}
}

// send hands item to the workers, applying back-pressure: when the queue is
// full it blocks until a worker frees a slot or the cycle starts draining. It
// returns false - without having sent - once draining has started or the queue
// has been closed, and it never panics: the read lock it holds for the
// duration of the send keeps close from running concurrently.
func (q *cycleQueue) send(item inboundMessage) bool {
	// Prefer the drain signal when it is already set: a select picks randomly
	// among ready cases, and a send into a queue the cycle is about to close is
	// the one choice we must not make.
	select {
	case <-q.draining:
		return false
	default:
	}

	q.mu.RLock()
	defer q.mu.RUnlock()
	if q.closed {
		return false
	}
	select {
	case q.ch <- item:
		return true
	case <-q.draining:
		return false
	}
}

// startDraining is the first step of teardown: it releases every producer that
// is blocked on a full queue (and turns away every later send) without taking
// the lock, so close can acquire it afterwards. Idempotent.
func (q *cycleQueue) startDraining() {
	q.drainOnce.Do(func() { close(q.draining) })
}

// close is the last step of teardown: it ends the workers' feed. It waits for
// every in-flight send to finish, which startDraining guarantees they will, and
// is idempotent.
func (q *cycleQueue) close() {
	q.startDraining()
	q.mu.Lock()
	defer q.mu.Unlock()
	if !q.closed {
		q.closed = true
		close(q.ch)
	}
}
