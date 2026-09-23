package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/interpreter"
	"github.com/hashicorp/go-hclog"
)

// fakeMQTTMessage implements mqtt.Message with an Ack that records when it was
// called and what the caller could observe at that moment.
type fakeMQTTMessage struct {
	payload []byte
	dup     bool
	mu      sync.Mutex
	acks    int
	onAck   func()
}

func (m *fakeMQTTMessage) Duplicate() bool   { return m.dup }
func (m *fakeMQTTMessage) Qos() byte         { return 1 }
func (m *fakeMQTTMessage) Retained() bool    { return false }
func (m *fakeMQTTMessage) Topic() string     { return "devices/d/messages/devicebound/" }
func (m *fakeMQTTMessage) MessageID() uint16 { return 1 }
func (m *fakeMQTTMessage) Payload() []byte   { return m.payload }
func (m *fakeMQTTMessage) Ack() {
	m.mu.Lock()
	m.acks++
	m.mu.Unlock()
	if m.onAck != nil {
		m.onAck()
	}
}

func (m *fakeMQTTMessage) ackCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.acks
}

// funcExecutor runs an arbitrary function as the command executor.
type funcExecutor struct {
	fn func(ctx context.Context) []byte
}

func (e *funcExecutor) AlwaysPostback() bool { return false }

func (e *funcExecutor) Execute(
	ctx context.Context,
	_ *interpreter.Message,
	_ agent.Device,
	_ hclog.Logger,
	_ agent.SystemInfoProvider,
	_ agent.DomainInfoProvider,
) []byte {
	return e.fn(ctx)
}

func svcWithJournal(t *testing.T, exec interpreter.Executor) *serviceContext {
	t.Helper()
	svc := newTestSvc(exec)
	svc.journal = newCommandJournal(filepath.Join(t.TempDir(), "command_journal"), 100, time.Hour)
	return svc
}

// svcWithBrokenJournal points the journal at a path that is a regular file, so
// every write fails at MkdirAll.
func svcWithBrokenJournal(t *testing.T, exec interpreter.Executor) *serviceContext {
	t.Helper()
	svc := newTestSvc(exec)
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc.journal = newCommandJournal(filepath.Join(blocker, "command_journal"), 100, time.Hour)
	return svc
}

func has(msgs []string, prefix string) int {
	n := 0
	for _, m := range msgs {
		if strings.HasPrefix(m, prefix) {
			n++
		}
	}
	return n
}

// ── receiveMessage ───────────────────────────────────────────────────────────

func TestReceiveMessage_JournalsBeforeAckingThenEnqueues(t *testing.T) {
	svc := svcWithJournal(t, &countingExecutor{})
	logger := hclog.NewNullLogger()
	notifier := &recordingNotifierWrapper{}
	queue := make(chan inboundMessage, 1)
	draining := make(chan struct{})

	payload := postbackPayload("echo hi", "id:1")
	key := journalKey(payload)
	journaledAtAck := false
	msg := &fakeMQTTMessage{payload: payload}
	msg.onAck = func() { journaledAtAck = fileExists(svc.journal.pendingPath(key)) }

	svc.receiveMessage(msg, queue, draining, 1, logger, notifier)

	if msg.ackCount() != 1 {
		t.Fatalf("acked %d times, want exactly once", msg.ackCount())
	}
	if !journaledAtAck {
		t.Error("the message was acknowledged before its journal entry existed")
	}
	select {
	case item := <-queue:
		if item.Key != key {
			t.Errorf("enqueued item carries key %q, want %q", item.Key, key)
		}
		if string(item.Payload) != string(payload) {
			t.Error("enqueued payload differs from the received one")
		}
	default:
		t.Fatal("message was not enqueued")
	}
	if n := svc.droppedMessages.Load(); n != 0 {
		t.Errorf("dropped counter = %d, want 0", n)
	}
}

func TestReceiveMessage_RedeliveryIsAckedAndNotEnqueued(t *testing.T) {
	svc := svcWithJournal(t, &countingExecutor{})
	logger := hclog.NewNullLogger()
	notifier := &recordingNotifierWrapper{}
	queue := make(chan inboundMessage, 2)
	draining := make(chan struct{})
	payload := postbackPayload("echo hi", "id:dup")

	svc.receiveMessage(&fakeMQTTMessage{payload: payload}, queue, draining, 2, logger, notifier)
	dup := &fakeMQTTMessage{payload: payload, dup: true}
	svc.receiveMessage(dup, queue, draining, 2, logger, notifier)

	if dup.ackCount() != 1 {
		t.Errorf("redelivery acked %d times, want 1", dup.ackCount())
	}
	if len(queue) != 1 {
		t.Errorf(
			"queue holds %d items, want 1: the redelivery must not be executed again",
			len(queue),
		)
	}

	// The same holds once the first delivery has completed (tombstone).
	item := <-queue
	if err := svc.journal.complete(item.Key); err != nil {
		t.Fatal(err)
	}
	late := &fakeMQTTMessage{payload: payload, dup: true}
	svc.receiveMessage(late, queue, draining, 2, logger, notifier)
	if late.ackCount() != 1 || len(queue) != 0 {
		t.Errorf(
			"late redelivery after completion: acks=%d queued=%d, want 1 and 0",
			late.ackCount(),
			len(queue),
		)
	}
}

func TestReceiveMessage_JournalFailureDegradesToInMemoryAndReportsOnce(t *testing.T) {
	svc := svcWithBrokenJournal(t, &countingExecutor{})
	logger := hclog.NewNullLogger()
	notifier := &recordingNotifierWrapper{}
	queue := make(chan inboundMessage, 4)
	draining := make(chan struct{})

	for i := 0; i < 3; i++ {
		m := &fakeMQTTMessage{payload: postbackPayload("echo hi", "id:"+string(rune('a'+i)))}
		svc.receiveMessage(m, queue, draining, 4, logger, notifier)
		if m.ackCount() != 1 {
			t.Errorf(
				"message %d acked %d times while degraded, want 1 (commands must still run)",
				i,
				m.ackCount(),
			)
		}
	}
	if len(queue) != 3 {
		t.Errorf("queued %d, want 3: a journal failure must not stop commands", len(queue))
	}
	for len(queue) > 0 {
		if item := <-queue; item.Key != "" {
			t.Error("an item accepted while the journal was failing carries a key")
		}
	}
	if n := has(notifier.all(), "AgentCommandJournalDegraded"); n != 1 {
		t.Errorf("degraded reported %d times across 3 failures, want exactly once", n)
	}
	if !svc.journalDegraded.Load() {
		t.Error("journalDegraded not set")
	}

	// Recovery: swap in a working journal; the next success clears the flag once.
	svc.journal = newCommandJournal(filepath.Join(t.TempDir(), "j"), 100, time.Hour)
	recovered := &fakeMQTTMessage{payload: postbackPayload("echo hi", "id:z")}
	svc.receiveMessage(recovered, queue, draining, 4, logger, notifier)
	if svc.journalDegraded.Load() {
		t.Error("journalDegraded still set after a successful write")
	}
}

func TestReceiveMessage_DrainingJournaledIsAckedAndKeptForReplay(t *testing.T) {
	svc := svcWithJournal(t, &countingExecutor{})
	logger := hclog.NewNullLogger()
	notifier := &recordingNotifierWrapper{}
	queue := make(chan inboundMessage, 1)
	queue <- inboundMessage{Payload: validPayload("echo full")}
	draining := make(chan struct{})
	close(draining)

	payload := postbackPayload("echo late", "id:late")
	m := &fakeMQTTMessage{payload: payload}
	svc.receiveMessage(m, queue, draining, 1, logger, notifier)

	if m.ackCount() != 1 {
		t.Errorf("journaled message arriving during teardown acked %d times, want 1", m.ackCount())
	}
	if n := svc.droppedMessages.Load(); n != 0 {
		t.Errorf("dropped counter = %d, want 0: a journaled message is deferred, not dropped", n)
	}
	if has(notifier.all(), "AgentMessageDropped") != 0 {
		t.Error("a drop notification was sent for a message that will be replayed")
	}
	fresh, _, err := svc.journal.pending()
	if err != nil || len(fresh) != 1 || fresh[0].Key != journalKey(payload) {
		t.Errorf("entry not left pending for replay: %v %v", keysOf(fresh), err)
	}
}

func TestReceiveMessage_DrainingUnjournaledIsLeftUnackedAndCounted(t *testing.T) {
	svc := svcWithBrokenJournal(t, &countingExecutor{})
	logger := hclog.NewNullLogger()
	notifier := &recordingNotifierWrapper{}
	queue := make(chan inboundMessage, 1)
	queue <- inboundMessage{Payload: validPayload("echo full")}
	draining := make(chan struct{})
	close(draining)

	m := &fakeMQTTMessage{payload: postbackPayload("echo lost", "id:lost")}
	svc.receiveMessage(m, queue, draining, 1, logger, notifier)

	if m.ackCount() != 0 {
		t.Errorf(
			"acked %d times, want 0: an unjournaled, unenqueued message must be left for the broker to redeliver",
			m.ackCount(),
		)
	}
	if n := svc.droppedMessages.Load(); n != 1 {
		t.Errorf("dropped counter = %d, want 1", n)
	}
	if has(notifier.all(), "AgentMessageDropped") != 1 {
		t.Error("expected exactly one drop notification")
	}
}

// ── processInbound ───────────────────────────────────────────────────────────

func TestProcessInbound_MarksStartedBeforeExecuteAndCompletesAfter(t *testing.T) {
	var svc *serviceContext
	payload := validPayload("echo hi") // no post_id: settles without a postback
	key := journalKey(payload)
	startedDuringExecute := false
	exec := &funcExecutor{fn: func(context.Context) []byte {
		fresh, _, _ := svc.journal.pending()
		for _, e := range fresh {
			if e.Key == key && !e.StartedAt.IsZero() {
				startedDuringExecute = true
			}
		}
		return []byte(`{}`)
	}}
	svc = svcWithJournal(t, exec)
	if _, err := svc.journal.put(key, payload); err != nil {
		t.Fatal(err)
	}

	svc.processInbound(
		inboundMessage{Payload: payload, Key: key},
		context.Background(), agent.Device{}, hclog.NewNullLogger(), &recordingNotifierWrapper{},
	)

	if !startedDuringExecute {
		t.Error("entry was not marked started before execution began")
	}
	if fileExists(svc.journal.pendingPath(key)) {
		t.Error("entry still pending after the command settled")
	}
	if !fileExists(svc.journal.donePath(key)) {
		t.Error("no tombstone after the command settled")
	}
}

func TestProcessInbound_CancelledMidExecutionLeavesEntryStartedForReplay(t *testing.T) {
	payload := validPayload("sleep")
	key := journalKey(payload)
	exec := &funcExecutor{fn: func(ctx context.Context) []byte {
		<-ctx.Done() // the cycle ends while the command is running
		return []byte(`{"error":"cancelled"}`)
	}}
	svc := svcWithJournal(t, exec)
	if _, err := svc.journal.put(key, payload); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		svc.processInbound(
			inboundMessage{Payload: payload, Key: key},
			ctx,
			agent.Device{},
			hclog.NewNullLogger(),
			&recordingNotifierWrapper{},
		)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	fresh, _, err := svc.journal.pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 1 || fresh[0].Key != key || fresh[0].StartedAt.IsZero() {
		t.Errorf("expected the entry to remain pending and marked started; got %+v", fresh)
	}
	if fileExists(svc.journal.donePath(key)) {
		t.Error("a cancelled command was tombstoned as if it had settled")
	}
}

// ── replayJournal ────────────────────────────────────────────────────────────

func TestReplayJournal_ExecutesUnstartedReportsStartedDiscardsExpired(t *testing.T) {
	var gotBodies []string
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotBodies = append(gotBodies, string(b))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	svc := svcWithJournal(t, &countingExecutor{})
	svc.HTTPClient = &http.Client{Transport: &schemeRewriteTransport{scheme: "http"}}
	svc.PostbackMaxAttempts = 1
	svc.PostbackBaseRetryBackoff = time.Millisecond
	device := deviceWithEngine(srv.Listener.Addr().String())

	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	svc.journal.now = func() time.Time { return now }

	// An entry the broker itself would have expired.
	expiredPayload := postbackPayload("echo old", "id:expired")
	if _, err := svc.journal.put(journalKey(expiredPayload), expiredPayload); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)

	// One that had started when the process died, one that had not.
	startedPayload := postbackPayload("echo started", "id:started")
	if _, err := svc.journal.put(journalKey(startedPayload), startedPayload); err != nil {
		t.Fatal(err)
	}
	if err := svc.journal.markStarted(journalKey(startedPayload)); err != nil {
		t.Fatal(err)
	}
	queuedPayload := postbackPayload("echo queued", "id:queued")
	if _, err := svc.journal.put(journalKey(queuedPayload), queuedPayload); err != nil {
		t.Fatal(err)
	}

	queue := make(chan inboundMessage, 4)
	notifier := &recordingNotifierWrapper{}
	fresh, expired := svc.snapshotJournal(hclog.NewNullLogger())
	svc.replayJournal(
		context.Background(),
		fresh,
		expired,
		queue,
		make(chan struct{}),
		4,
		device,
		hclog.NewNullLogger(),
		notifier,
	)

	// Unstarted -> enqueued for execution, still journaled until it settles.
	select {
	case item := <-queue:
		if item.Key != journalKey(queuedPayload) {
			t.Errorf("enqueued %q, want the unstarted entry", item.Key)
		}
	default:
		t.Fatal("the unstarted entry was not enqueued")
	}
	if len(queue) != 0 {
		t.Errorf(
			"%d extra items enqueued; started and expired entries must never be executed",
			len(queue),
		)
	}

	// Started -> reported to the engine as interrupted, then completed.
	mu.Lock()
	bodies := append([]string(nil), gotBodies...)
	mu.Unlock()
	if len(bodies) != 1 {
		t.Fatalf(
			"expected exactly one postback (the interrupted report), got %d: %v",
			len(bodies),
			bodies,
		)
	}
	var r struct {
		Error       string `json:"error"`
		Interrupted bool   `json:"interrupted"`
	}
	if err := json.Unmarshal([]byte(bodies[0]), &r); err != nil {
		t.Fatal(err)
	}
	if !r.Interrupted || !strings.Contains(r.Error, "interrupted") {
		t.Errorf("interrupted report body = %s", bodies[0])
	}
	if !fileExists(svc.journal.donePath(journalKey(startedPayload))) {
		t.Error("started entry was not completed after being reported")
	}
	if has(notifier.all(), "AgentCommandInterrupted:") != 1 {
		t.Error("expected one AgentCommandInterrupted notification")
	}

	// Expired -> discarded without execution or tombstone, and notified.
	if fileExists(svc.journal.pendingPath(journalKey(expiredPayload))) ||
		fileExists(svc.journal.donePath(journalKey(expiredPayload))) {
		t.Error("expired entry was not discarded cleanly")
	}
	if has(notifier.all(), "AgentCommandExpired:") != 1 {
		t.Error("expected one AgentCommandExpired notification")
	}
}

func TestReplayJournal_StopsAtTeardownLeavingRestJournaled(t *testing.T) {
	svc := svcWithJournal(t, &countingExecutor{})
	for _, id := range []string{"a", "b", "c"} {
		p := postbackPayload("echo "+id, "id:"+id)
		if _, err := svc.journal.put(journalKey(p), p); err != nil {
			t.Fatal(err)
		}
	}
	queue := make(chan inboundMessage, 1)
	queue <- inboundMessage{Payload: validPayload("echo full")}
	draining := make(chan struct{})
	close(draining)

	fresh, expired := svc.snapshotJournal(hclog.NewNullLogger())
	svc.replayJournal(
		context.Background(),
		fresh,
		expired,
		queue,
		draining,
		1,
		agent.Device{},
		hclog.NewNullLogger(),
		&recordingNotifierWrapper{},
	)

	fresh, _, err := svc.journal.pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh) != 3 {
		t.Errorf("%d entries pending after a replay that could not enqueue, want all 3", len(fresh))
	}
}

func TestReplayJournal_NoJournalIsANoop(t *testing.T) {
	svc := newTestSvc(&countingExecutor{})
	fresh, expired := svc.snapshotJournal(hclog.NewNullLogger())
	if fresh != nil || expired != nil {
		t.Fatalf("snapshot without a journal = %v/%v, want nil/nil", fresh, expired)
	}
	svc.replayJournal(
		context.Background(),
		fresh,
		expired,
		make(chan inboundMessage, 1),
		make(chan struct{}),
		1,
		agent.Device{},
		hclog.NewNullLogger(),
		&recordingNotifierWrapper{},
	)
}

// The race behind run 35745811929: the cycle subscribed, a command arrived and
// a worker started it, and only then did replay list the journal - so it saw a
// started entry and reported a running command as interrupted (and would have
// enqueued a queued one a second time). Replay must act only on the snapshot
// taken before the cycle could receive anything.
func TestReplayJournal_IgnoresEntriesAcceptedAfterTheSnapshot(t *testing.T) {
	engineHits := 0
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		engineHits++
		w.WriteHeader(http.StatusOK)
	}))
	defer engine.Close()
	svc := svcWithJournal(t, &countingExecutor{})
	svc.HTTPClient = &http.Client{Transport: &schemeRewriteTransport{scheme: "http"}}

	// The snapshot a cycle takes before Connect: nothing left over.
	fresh, expired := svc.snapshotJournal(hclog.NewNullLogger())
	if len(fresh) != 0 || len(expired) != 0 {
		t.Fatalf("expected an empty snapshot, got %d fresh / %d expired", len(fresh), len(expired))
	}

	// Then the cycle accepts two commands: one a worker has started, one still
	// queued. Both belong to this cycle, not to replay.
	running := postbackPayload("sleep 30", "id:running")
	runningKey := journalKey(running)
	if _, err := svc.journal.put(runningKey, running); err != nil {
		t.Fatal(err)
	}
	if err := svc.journal.markStarted(runningKey); err != nil {
		t.Fatal(err)
	}
	queued := postbackPayload("echo queued", "id:queued")
	if _, err := svc.journal.put(journalKey(queued), queued); err != nil {
		t.Fatal(err)
	}

	queue := make(chan inboundMessage, 4)
	notifier := &recordingNotifierWrapper{}
	svc.replayJournal(
		context.Background(),
		fresh,
		expired,
		queue,
		make(chan struct{}),
		4,
		deviceWithEngine(strings.TrimPrefix(engine.URL, "http://")),
		hclog.NewNullLogger(),
		notifier,
	)

	if len(queue) != 0 {
		t.Errorf(
			"replay enqueued %d command(s) this cycle had already accepted; want 0",
			len(queue),
		)
	}
	if engineHits != 0 {
		t.Errorf(
			"replay reported %d command(s) to the engine; the running one was not interrupted",
			engineHits,
		)
	}
	if has(notifier.all(), "AgentCommandInterrupted") != 0 {
		t.Error("replay notified AgentCommandInterrupted for a command that is still running")
	}
	stillFresh, _, err := svc.journal.pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(stillFresh) != 2 {
		t.Fatalf(
			"journal has %d pending entries after replay, want both untouched",
			len(stillFresh),
		)
	}
	for _, e := range stillFresh {
		if e.Key == runningKey && e.StartedAt.IsZero() {
			t.Error("the running entry lost its started mark")
		}
	}
	if fileExists(svc.journal.donePath(runningKey)) {
		t.Error("replay completed the running command's journal entry")
	}
}

// ── worker lifetime across cycle ends (sc-118039) ────────────────────────────

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A cycle ending for a SAS renewal or a lost connection closes the queue; the
// command that is executing must keep running, finish, post back and complete
// its journal entry. Before this it was killed by the cycle's cancellation.
func TestStartWorkers_RunningCommandSurvivesCycleEnd(t *testing.T) {
	var started sync.Once
	startedCh := make(chan struct{})
	release := make(chan struct{})
	var cancelled atomic.Bool
	exec := &funcExecutor{fn: func(ctx context.Context) []byte {
		started.Do(func() { close(startedCh) })
		select {
		case <-release:
			return []byte(`{"error":"","output":"done"}`)
		case <-ctx.Done():
			cancelled.Store(true)
			return []byte(`{"error":"cancelled","output":""}`)
		}
	}}
	var postbacks atomic.Int32
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		postbacks.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer engine.Close()
	svc := svcWithJournal(t, exec)
	svc.HTTPClient = &http.Client{Transport: &schemeRewriteTransport{scheme: "http"}}
	device := deviceWithEngine(strings.TrimPrefix(engine.URL, "http://"))

	queue := make(chan inboundMessage, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	svc.startWorkers(ctx, queue, 1, device, hclog.NewNullLogger(), &recordingNotifierWrapper{})

	payload := postbackPayload("long running", "id:survive")
	key := journalKey(payload)
	if _, err := svc.journal.put(key, payload); err != nil {
		t.Fatal(err)
	}
	svc.owned.Store(key, struct{}{})
	queue <- inboundMessage{Payload: payload, Key: key}
	<-startedCh

	close(queue) // the cycle ends: renewal or lost connection, not a service stop

	time.Sleep(50 * time.Millisecond)
	if cancelled.Load() {
		t.Fatal("the running command was cancelled by the cycle ending")
	}
	if postbacks.Load() != 0 || fileExists(svc.journal.donePath(key)) {
		t.Fatal("the command was settled before it finished")
	}

	close(release)
	waitFor(t, "the postback", func() bool { return postbacks.Load() == 1 })
	waitFor(
		t,
		"the journal entry to complete",
		func() bool { return fileExists(svc.journal.donePath(key)) },
	)
	if _, live := svc.owned.Load(key); live {
		t.Error("completed command is still marked owned")
	}
	if cancelled.Load() {
		t.Error("the command observed a cancellation")
	}

	svc.stopWorkers(cancel, hclog.NewNullLogger())
}

// Commands still queued when the cycle ends are run by the outgoing pool, not
// dropped, and are not re-enqueued by the next cycle because they are owned.
func TestStartWorkers_QueuedCommandsRunAfterCycleEnd(t *testing.T) {
	exec := &countingExecutor{result: []byte(`{"error":"","output":"ok"}`)}
	svc := svcWithJournal(t, exec)
	queue := make(chan inboundMessage, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var keys []string
	for _, cmd := range []string{"echo one", "echo two", "echo three"} {
		payload := validPayload(cmd) // no post_id: settles without a postback
		key := journalKey(payload)
		if _, err := svc.journal.put(key, payload); err != nil {
			t.Fatal(err)
		}
		svc.owned.Store(key, struct{}{})
		queue <- inboundMessage{Payload: payload, Key: key}
		keys = append(keys, key)
	}
	// The next cycle's snapshot, taken while these are queued, must skip them.
	if fresh, _ := svc.snapshotJournal(hclog.NewNullLogger()); len(fresh) != 0 {
		t.Fatalf("snapshot returned %d owned entries; want 0", len(fresh))
	}

	svc.startWorkers(
		ctx,
		queue,
		1,
		agent.Device{},
		hclog.NewNullLogger(),
		&recordingNotifierWrapper{},
	)
	close(queue) // cycle ends with work still queued
	waitFor(t, "all queued commands to run", func() bool { return exec.count.Load() == 3 })
	for _, key := range keys {
		waitFor(t, "journal entry "+key+" to complete", func() bool {
			return fileExists(svc.journal.donePath(key))
		})
	}
	svc.stopWorkers(cancel, hclog.NewNullLogger())
}

// The snapshot is for what a previous process left behind. A key this process
// accepted - via receiveMessage - is skipped; a leftover with no owner is kept.
func TestSnapshotJournal_SkipsKeysOwnedByThisProcess(t *testing.T) {
	svc := svcWithJournal(t, &countingExecutor{})
	logger := hclog.NewNullLogger()
	queue := make(chan inboundMessage, 4)
	msg := &fakeMQTTMessage{payload: postbackPayload("echo mine", "id:mine")}
	svc.receiveMessage(msg, queue, make(chan struct{}), 4, logger, &recordingNotifierWrapper{})

	leftover := postbackPayload("echo theirs", "id:theirs")
	if _, err := svc.journal.put(journalKey(leftover), leftover); err != nil {
		t.Fatal(err)
	}

	fresh, expired := svc.snapshotJournal(logger)
	if len(expired) != 0 {
		t.Errorf("expired = %d, want 0", len(expired))
	}
	if len(fresh) != 1 || fresh[0].Key != journalKey(leftover) {
		t.Fatalf("snapshot = %v, want only the leftover %q", keysOf(fresh), journalKey(leftover))
	}
}

// Only a service stop cancels a running command. stopWorkers must return once
// the pools have exited, and the cancelled command stays "started" in the
// journal for the next process to report as interrupted.
func TestStopWorkers_CancelsRunningCommandAndWaits(t *testing.T) {
	startedCh := make(chan struct{})
	var started sync.Once
	exec := &funcExecutor{fn: func(ctx context.Context) []byte {
		started.Do(func() { close(startedCh) })
		<-ctx.Done()
		return []byte(`{"error":"cancelled","output":""}`)
	}}
	svc := svcWithJournal(t, exec)
	queue := make(chan inboundMessage, 1)
	ctx, cancel := context.WithCancel(context.Background())
	svc.startWorkers(
		ctx,
		queue,
		1,
		agent.Device{},
		hclog.NewNullLogger(),
		&recordingNotifierWrapper{},
	)

	payload := postbackPayload("sleep forever", "id:stop")
	key := journalKey(payload)
	if _, err := svc.journal.put(key, payload); err != nil {
		t.Fatal(err)
	}
	svc.owned.Store(key, struct{}{})
	queue <- inboundMessage{Payload: payload, Key: key}
	<-startedCh

	begun := time.Now()
	svc.stopWorkers(cancel, hclog.NewNullLogger())
	if took := time.Since(begun); took > 5*time.Second {
		t.Fatalf("stopWorkers took %s; the cancelled worker should exit at once", took)
	}
	if fileExists(svc.journal.donePath(key)) {
		t.Error(
			"a command cancelled by the service stop was completed; it must stay started so the next process reports it",
		)
	}
}
