package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/interpreter"
	"github.com/RewstApp/agent-smith-go/internal/mqtt"
	"github.com/RewstApp/agent-smith-go/plugins"
	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/hashicorp/go-hclog"
)

// The fleet-health object (sc-129687) is assembled from counters the service
// already keeps and published to the device twin on change, coalesced to the
// configured interval.

type statsExecutor struct {
	funcExecutor
	stats interpreter.ExecutionStats
}

func (e *statsExecutor) ExecutionStats() interpreter.ExecutionStats { return e.stats }

type pluginStatsNotifier struct {
	mockNotifierWrapper
	plugins []plugins.PluginStats
}

func (n *pluginStatsNotifier) PluginStats() []plugins.PluginStats { return n.plugins }

// capturingPublisher records every reported-properties publish.
type capturingPublisher struct {
	mu        sync.Mutex
	published []mqtt.ReportedProperties
	attempts  int
	err       error
}

func (c *capturingPublisher) publish(_ pahomqtt.Client, props mqtt.ReportedProperties) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.attempts++
	if c.err != nil {
		return c.err
	}
	c.published = append(c.published, props)
	return nil
}

func (c *capturingPublisher) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.published)
}

func (c *capturingPublisher) attemptCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.attempts
}

func (c *capturingPublisher) last() mqtt.ReportedProperties {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.published[len(c.published)-1]
}

// waitForCount polls until the publisher has published n times or the wait
// runs out, and reports whether it did.
func (c *capturingPublisher) waitForCount(n int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for c.count() < n && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond) // sleep-ok: poll interval inside a bounded wait
	}
	return c.count() >= n
}

func newHealthTestService(t *testing.T) (*serviceContext, *capturingPublisher) {
	t.Helper()
	dir := t.TempDir()
	svc := &serviceContext{
		OrgId: "health-org",
		Executor: &statsExecutor{
			stats: interpreter.ExecutionStats{Completed: 7, TimedOut: 2, Failed: 1},
		},
		startedAt:     time.Now().Add(-90 * time.Second),
		healthFileDir: dir,
	}
	svc.journal = newCommandJournal(filepath.Join(dir, "command_journal"), 10, time.Hour)
	svc.spool = newPostbackSpool(
		filepath.Join(dir, "postback_spool"),
		10,
		time.Hour,
		3,
		hclog.NewNullLogger(),
	)
	pub := &capturingPublisher{}
	svc.publishHealth = pub.publish
	return svc, pub
}

func TestHealthSnapshot_GathersEveryCounter(t *testing.T) {
	svc, _ := newHealthTestService(t)
	if _, err := svc.journal.put("post:a", []byte(`{"post_id":"a"}`)); err != nil {
		t.Fatal(err)
	}
	svc.journalDegraded.Store(true)
	svc.journalExpired.Store(3)
	svc.journalInterrupted.Store(1)
	if err := svc.spool.enqueue(
		spoolEntry{PostId: "s1", Result: []byte("{}"), CreatedAt: time.Now()},
	); err != nil {
		t.Fatal(err)
	}
	svc.spool.countDrop(dropReasonAttempts)
	svc.executing.Store(2)
	svc.connectedAt.Store(time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC).UnixNano())
	svc.recordConnectionError(errors.New("EOF"))
	notifier := &pluginStatsNotifier{plugins: []plugins.PluginStats{
		{
			Name:          "teams",
			NotifierStats: plugins.NotifierStats{Restarts: 4, NotifyFailures: 5, NotifyTimeouts: 1},
		},
	}}

	h := svc.healthSnapshot(notifier)

	if h.UptimeSeconds < 89 || h.UptimeSeconds > 95 {
		t.Errorf("uptime %d, want ~90", h.UptimeSeconds)
	}
	if h.ConnectedAt != "2026-10-26T12:00:00Z" || h.LastConnectionError != "EOF" {
		t.Errorf("connection: %q %q", h.ConnectedAt, h.LastConnectionError)
	}
	want := mqtt.JournalHealth{Pending: 1, Degraded: true, ExpiredTotal: 3, InterruptedTotal: 1}
	if h.Journal != want {
		t.Errorf("journal %+v, want %+v", h.Journal, want)
	}
	if h.Spool.Entries != 1 || h.Spool.DroppedAttempts != 1 {
		t.Errorf("spool %+v", h.Spool)
	}
	want2 := mqtt.CommandHealth{Executing: 2, CompletedTotal: 7, TimedOutTotal: 2, FailedTotal: 1}
	if h.Commands != want2 {
		t.Errorf("commands %+v", h.Commands)
	}
	if len(h.Plugins) != 1 ||
		h.Plugins[0] != (mqtt.PluginHealth{Name: "teams", Restarts: 4, NotifyFailures: 5, NotifyTimeouts: 1}) {
		t.Errorf("plugins %+v", h.Plugins)
	}
	if _, err := time.Parse(time.RFC3339, h.ReportedAt); err != nil {
		t.Errorf("reported_at %q: %v", h.ReportedAt, err)
	}
}

func TestHealthSnapshot_WithoutJournalSpoolOrStats(t *testing.T) {
	svc := &serviceContext{OrgId: "bare", Executor: &funcExecutor{}}
	h := svc.healthSnapshot(&mockNotifierWrapper{})
	if h.Journal != (mqtt.JournalHealth{}) || h.Spool != (mqtt.SpoolHealth{}) ||
		h.Commands != (mqtt.CommandHealth{}) {
		t.Errorf("bare snapshot carries counters: %+v", h)
	}
	if h.Plugins == nil {
		t.Error("plugins must serialise as an empty list, not null")
	}
	if h.UptimeSeconds != 0 || h.ConnectedAt != "" {
		t.Errorf("bare snapshot: uptime %d connected %q", h.UptimeSeconds, h.ConnectedAt)
	}
}

func TestReportHealth_PublishesOnChangeAndWritesTheFile(t *testing.T) {
	svc, pub := newHealthTestService(t)
	notifier := &mockNotifierWrapper{}
	logger := hclog.NewNullLogger()
	dataDir := svc.healthDir()

	if !svc.reportHealth(context.Background(), nil, notifier, logger, reportConnected) {
		t.Fatal("the connected report was not published")
	}
	if svc.reportHealth(context.Background(), nil, notifier, logger, reportHealthTick) {
		t.Error("an unchanged health was published again")
	}
	svc.spool.countDrop(dropReasonExpired)
	if !svc.reportHealth(context.Background(), nil, notifier, logger, reportHealthTick) {
		t.Error("a changed counter was not published")
	}
	if pub.count() != 2 {
		t.Errorf("published %d times, want 2", pub.count())
	}
	if pub.published[1].AgentVersion == "" || pub.published[1].Health.Spool.DroppedExpired != 1 {
		t.Errorf("published %+v", pub.published[1])
	}
	// Every publish carries the commands object, empty when idle, so a list a
	// previous process left in the twin is always overwritten (sc-130762).
	for i, p := range pub.published {
		if p.Commands == nil || len(p.Commands.InFlight) != 0 {
			t.Errorf("publish %d commands object %+v, want an empty list", i, p.Commands)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dataDir, healthFileName))
	if err != nil {
		t.Fatalf("health file: %v", err)
	}
	var fromFile mqtt.ReportedProperties
	if err := json.Unmarshal(raw, &fromFile); err != nil || fromFile.Health == nil ||
		fromFile.Health.Spool.DroppedExpired != 1 || fromFile.AgentVersion == "" {
		t.Errorf("health file %s: %v", raw, err)
	}
	if got, err := readHealthFileFrom(dataDir); err != nil || !got.Health.Same(fromFile.Health) {
		t.Errorf("readHealthFileFrom: %+v %v", got, err)
	}
	if _, err := readHealthFileFrom(t.TempDir()); err == nil {
		t.Error("a directory without a health file read as one")
	}
}

func TestReportHealth_PublishFailureIsNotFatalAndIsRetriedNextTime(t *testing.T) {
	svc, pub := newHealthTestService(t)
	pub.err = errors.New("broker went away")
	logger := hclog.NewNullLogger()
	if svc.reportHealth(
		context.Background(),
		nil,
		&mockNotifierWrapper{},
		logger,
		reportConnected,
	) {
		t.Fatal("a failed publish was reported as published")
	}
	pub.err = nil
	if !svc.reportHealth(
		context.Background(),
		nil,
		&mockNotifierWrapper{},
		logger,
		reportHealthTick,
	) {
		t.Error(
			"after a failed publish the next unchanged check should publish, since nothing was recorded",
		)
	}
}

// A failed publish holds the clearing kicks until a tick succeeds: otherwise
// every command finishing against a broker that is not acknowledging would
// start another blocking attempt and log another Warn.
func TestReportHealth_FailedPublishHoldsKicksUntilATickSucceeds(t *testing.T) {
	svc, pub := newHealthTestService(t)
	exec := &progressExecutor{}
	svc.Executor = exec
	ctx := context.Background()
	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	exec.set(interpreter.Progress{PostID: "hold-1", StartedAt: time.Now()})
	if !svc.reportHealth(ctx, nil, notifier, logger, reportConnected) {
		t.Fatal("the connected report was not published")
	}
	pub.err = errors.New("broker not acknowledging")
	if svc.reportHealth(ctx, nil, notifier, logger, reportHeartbeat) {
		t.Fatal("a failed heartbeat was reported as published")
	}
	exec.set()
	attempts := pub.attemptCount()
	if svc.reportHealth(ctx, nil, notifier, logger, reportFinished) {
		t.Fatal("a kick after a failed publish published")
	}
	if pub.attemptCount() != attempts {
		t.Error("a kick after a failed publish attempted another publish")
	}
	pub.err = nil
	if !svc.reportHealth(ctx, nil, notifier, logger, reportHealthTick) {
		t.Fatal("the health tick after the failure did not publish")
	}
	if last := pub.last(); last.Commands == nil || len(last.Commands.InFlight) != 0 {
		t.Errorf("the tick after the failure did not carry the cleared list: %+v", last.Commands)
	}
}

func TestHealthLoop_CoalescesToTheIntervalAndStopsWithTheCycle(t *testing.T) {
	svc, pub := newHealthTestService(t)
	interval := 1
	device := agent.Device{
		HealthReportIntervalSeconds: &interval,
		CommandHeartbeatSeconds:     &interval,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.healthLoop(ctx, nil, device, &mockNotifierWrapper{}, hclog.NewNullLogger())
		close(done)
	}()
	// Several changes inside one interval become one publish on the next tick.
	svc.spool.countDrop(dropReasonExpired)
	svc.spool.countDrop(dropReasonCapacity)
	svc.spool.countDrop(dropReasonCorrupt)
	deadline := time.Now().Add(5 * time.Second)
	for pub.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond) // sleep-ok: poll interval inside a bounded wait
	}
	if pub.count() != 1 {
		t.Fatalf("published %d times after a burst of changes, want 1", pub.count())
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("healthLoop did not stop with the cycle context")
	}
}

func TestHealthSame_IgnoresTheClockFields(t *testing.T) {
	a := &mqtt.Health{ReportedAt: "t1", UptimeSeconds: 1, Journal: mqtt.JournalHealth{Pending: 2}}
	b := &mqtt.Health{ReportedAt: "t2", UptimeSeconds: 99, Journal: mqtt.JournalHealth{Pending: 2}}
	if !a.Same(b) {
		t.Error("objects differing only in time were not the same")
	}
	b.Journal.Degraded = true
	if a.Same(b) {
		t.Error("a changed counter read as the same")
	}
	c := &mqtt.Health{Plugins: []mqtt.PluginHealth{{Name: "x"}}}
	if a.Same(c) || c.Same(nil) {
		t.Error("plugin and nil differences were not detected")
	}
}

func TestReportHealth_DoesNothingOnceTheCycleEnded(t *testing.T) {
	svc, pub := newHealthTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if svc.reportHealth(ctx, nil, &mockNotifierWrapper{}, hclog.NewNullLogger(), reportConnected) {
		t.Fatal("a cancelled cycle published")
	}
	if pub.count() != 0 {
		t.Errorf("published %d times after cancellation", pub.count())
	}
	if _, err := os.Stat(filepath.Join(svc.healthDir(), healthFileName)); !os.IsNotExist(err) {
		t.Error("the health file was written after cancellation")
	}
}

// Progress heartbeats (sc-129689): a running command is published every tick
// with its elapsed time, the publish after it finishes clears the list, and a
// finished command kicks the reporter instead of waiting for the tick.

type progressExecutor struct {
	funcExecutor
	mu      sync.Mutex
	running []interpreter.Progress
}

func (e *progressExecutor) InFlight() []interpreter.Progress {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]interpreter.Progress(nil), e.running...)
}

func (e *progressExecutor) set(running ...interpreter.Progress) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.running = running
}

func TestProgressSnapshot_FormatsRunningCommands(t *testing.T) {
	exec := &progressExecutor{}
	svc := &serviceContext{OrgId: "p", Executor: exec}
	if got := svc.progressSnapshot(); got == nil || got.InFlight == nil || len(got.InFlight) != 0 {
		t.Fatalf("nothing running must report an empty list, got %+v", got)
	}
	started := time.Now().Add(-42 * time.Second)
	exec.set(
		interpreter.Progress{
			PostID:         "hold-1",
			StartedAt:      started,
			StdoutBytes:    7,
			StderrBytes:    1,
			LastOutputLine: "tick",
		},
	)
	got := svc.progressSnapshot()
	if got == nil || len(got.InFlight) != 1 {
		t.Fatalf("snapshot %+v", got)
	}
	c := got.InFlight[0]
	if c.PostID != "hold-1" || c.ElapsedSeconds < 41 || c.ElapsedSeconds > 44 ||
		c.StdoutBytes != 7 ||
		c.StderrBytes != 1 ||
		c.LastOutputLine != "tick" {
		t.Errorf("entry %+v", c)
	}
	if _, err := time.Parse(time.RFC3339, c.StartedAt); err != nil {
		t.Errorf("started_at %q: %v", c.StartedAt, err)
	}
	bare := (&serviceContext{Executor: &funcExecutor{}}).progressSnapshot()
	if bare == nil || len(bare.InFlight) != 0 {
		t.Errorf("an executor without progress must report an empty list, got %+v", bare)
	}
}

func TestReportHealth_PublishesWhileRunningAndClearsAfter(t *testing.T) {
	svc, pub := newHealthTestService(t)
	exec := &progressExecutor{}
	svc.Executor = exec
	ctx := context.Background()
	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	svc.reportHealth(ctx, nil, notifier, logger, reportConnected)

	if svc.reportHealth(ctx, nil, notifier, logger, reportHeartbeat) {
		t.Fatal("an idle heartbeat tick published")
	}
	exec.set(interpreter.Progress{PostID: "hold-1", StartedAt: time.Now()})
	if !svc.reportHealth(ctx, nil, notifier, logger, reportHeartbeat) {
		t.Fatal("a running command was not published")
	}
	if !svc.reportHealth(ctx, nil, notifier, logger, reportHeartbeat) {
		t.Fatal("a still-running command was not republished (its elapsed time moved)")
	}
	if svc.reportHealth(ctx, nil, notifier, logger, reportFinished) {
		t.Fatal("a kick while the command still runs published")
	}
	exec.set()
	if !svc.reportHealth(ctx, nil, notifier, logger, reportFinished) {
		t.Fatal("the publish that clears the list did not happen")
	}
	if svc.reportHealth(ctx, nil, notifier, logger, reportFinished) {
		t.Error("a second kick with nothing to clear published again")
	}
	if svc.reportHealth(ctx, nil, notifier, logger, reportHealthTick) {
		t.Error("an unchanged health was published again")
	}
	published := pub.published
	if n := len(published); n != 4 {
		t.Fatalf("published %d times, want 4", n)
	}
	if published[0].Commands == nil || len(published[0].Commands.InFlight) != 0 {
		t.Errorf("the idle first publish must carry an empty list, got %+v", published[0].Commands)
	}
	if published[1].Commands == nil || len(published[1].Commands.InFlight) != 1 ||
		published[1].Commands.InFlight[0].PostID != "hold-1" {
		t.Errorf("running publish %+v", published[1].Commands)
	}
	if published[3].Commands == nil || len(published[3].Commands.InFlight) != 0 {
		t.Errorf("the clearing publish must carry an empty list, got %+v", published[3].Commands)
	}
	if got, err := readHealthFileFrom(svc.healthDir()); err != nil || got.Commands == nil ||
		len(got.Commands.InFlight) != 0 {
		t.Errorf("health file after clearing: %+v %v", got, err)
	}
}

// A completion while other commands still run waits for the heartbeat: the
// kick publishes only the clearing update, so with several workers a stream
// of short commands beside a long one cannot publish at the spacing floor.
func TestReportHealth_KickPublishesOnlyTheClearingUpdate(t *testing.T) {
	svc, pub := newHealthTestService(t)
	exec := &progressExecutor{}
	svc.Executor = exec
	ctx := context.Background()
	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	long := interpreter.Progress{PostID: "long", StartedAt: time.Now()}
	exec.set(long, interpreter.Progress{PostID: "short-1", StartedAt: time.Now()})
	svc.reportHealth(ctx, nil, notifier, logger, reportConnected)
	exec.set(long)
	if svc.reportHealth(ctx, nil, notifier, logger, reportFinished) {
		t.Fatal("a completion beside a running command published")
	}
	if pub.count() != 1 {
		t.Fatalf("published %d times, want 1", pub.count())
	}
	if !svc.reportHealth(ctx, nil, notifier, logger, reportHeartbeat) {
		t.Fatal("the heartbeat did not publish the remaining command")
	}
	if last := pub.last(); len(last.Commands.InFlight) != 1 ||
		last.Commands.InFlight[0].PostID != "long" {
		t.Errorf("heartbeat list %+v", last.Commands.InFlight)
	}
}

// The health file is for --diagnostic and is rewritten only when the health
// object or the set of running commands changed, not on every heartbeat.
func TestReportHealth_WritesTheFileOnlyOnChange(t *testing.T) {
	svc, _ := newHealthTestService(t)
	exec := &progressExecutor{}
	svc.Executor = exec
	ctx := context.Background()
	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	path := filepath.Join(svc.healthDir(), healthFileName)
	exec.set(interpreter.Progress{PostID: "hold-1", StartedAt: time.Now().Add(-time.Minute)})
	svc.reportHealth(ctx, nil, notifier, logger, reportConnected)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Two heartbeats: the elapsed time moved, the file must not.
	svc.reportHealth(ctx, nil, notifier, logger, reportHeartbeat)
	svc.reportHealth(ctx, nil, notifier, logger, reportHeartbeat)
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Error("a heartbeat with the same running set rewrote the health file")
	}
	exec.set()
	svc.reportHealth(ctx, nil, notifier, logger, reportFinished)
	cleared, _ := os.ReadFile(path)
	if string(cleared) == string(before) {
		t.Error("the clearing publish did not rewrite the health file")
	}
	if got, err := readHealthFileFrom(svc.healthDir()); err != nil ||
		len(got.Commands.InFlight) != 0 {
		t.Errorf("health file after clearing: %+v %v", got, err)
	}
}

// startHealthLoop runs healthLoop in the background and waits until it has
// installed its kick channel; the returned stop cancels it and joins it.
func startHealthLoop(t *testing.T, svc *serviceContext, heartbeat, health int) (stop func()) {
	t.Helper()
	device := agent.Device{
		CommandHeartbeatSeconds:     &heartbeat,
		HealthReportIntervalSeconds: &health,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.healthLoop(ctx, nil, device, &mockNotifierWrapper{}, hclog.NewNullLogger())
		close(done)
	}()
	kickInstalled := func() bool {
		svc.healthMu.Lock()
		defer svc.healthMu.Unlock()
		return svc.healthKick != nil
	}
	deadline := time.Now().Add(5 * time.Second)
	for !kickInstalled() && time.Now().Before(deadline) {
		// sleep-ok: poll until the loop has installed its kick channel
		time.Sleep(10 * time.Millisecond)
	}
	return func() {
		cancel()
		<-done
	}
}

// A kick inside the spacing floor is deferred to the end of the floor, not
// dropped: the clearing update goes out about a second after the previous
// publish rather than at the next heartbeat tick.
func TestHealthLoop_KickInsideTheSpacingIsDeferredNotDropped(t *testing.T) {
	svc, pub := newHealthTestService(t)
	exec := &progressExecutor{}
	svc.Executor = exec
	exec.set(interpreter.Progress{PostID: "hold-1", StartedAt: time.Now()})
	svc.reportHealth(
		context.Background(),
		nil,
		&mockNotifierWrapper{},
		hclog.NewNullLogger(),
		reportConnected,
	)
	stop := startHealthLoop(t, svc, 60, 60)
	defer stop()
	// The command finishes right after the publish: inside the floor.
	exec.set()
	kicked := time.Now()
	svc.kickHealth()
	svc.kickHealth() // a second kick while one is deferred is absorbed
	if !pub.waitForCount(2, 5*time.Second) {
		t.Fatal("the deferred clearing publish never happened")
	}
	if elapsed := time.Since(kicked); elapsed < kickMinSpacing/2 || elapsed > 4*time.Second {
		t.Errorf(
			"the clearing publish came %s after the kick, want about %s",
			elapsed,
			kickMinSpacing,
		)
	}
	if last := pub.last(); len(last.Commands.InFlight) != 0 {
		t.Errorf("the clearing publish listed %+v", last.Commands.InFlight)
	}
	// sleep-ok: gives a duplicate publish time to happen before asserting it did not
	time.Sleep(200 * time.Millisecond)
	if pub.count() != 2 {
		t.Errorf("published %d times, want 2 (one clear for two kicks)", pub.count())
	}
}

// A kick outside the floor publishes at once, and a kick with nothing to
// clear does not publish at all.
func TestHealthLoop_KickPublishesAtOnceOnlyWhenThereIsAListToClear(t *testing.T) {
	svc, pub := newHealthTestService(t)
	exec := &progressExecutor{}
	svc.Executor = exec
	stop := startHealthLoop(t, svc, 60, 60)
	defer stop()
	// Nothing was ever listed: a short command finishing publishes nothing.
	svc.kickHealth()
	// sleep-ok: gives an unwanted publish time to happen before asserting it did not
	time.Sleep(200 * time.Millisecond)
	if pub.count() != 0 {
		t.Fatalf("a kick with nothing to clear published %d times", pub.count())
	}
	exec.set(interpreter.Progress{PostID: "hold-1", StartedAt: time.Now()})
	svc.reportHealth(
		context.Background(),
		nil,
		&mockNotifierWrapper{},
		hclog.NewNullLogger(),
		reportHeartbeat,
	)
	// sleep-ok: moves past the spacing floor so the kick publishes at once
	time.Sleep(kickMinSpacing + 100*time.Millisecond)
	exec.set()
	kicked := time.Now()
	svc.kickHealth()
	if !pub.waitForCount(2, 5*time.Second) {
		t.Fatal("the clearing publish never happened")
	}
	if elapsed := time.Since(kicked); elapsed > 500*time.Millisecond {
		t.Errorf("the clearing publish took %s, want at once", elapsed)
	}
}

// The health interval is honoured on its own ticker: a health interval below
// the heartbeat interval re-checks the counters at that shorter cadence.
func TestHealthLoop_HealthIntervalBelowTheHeartbeatIsHonoured(t *testing.T) {
	svc, pub := newHealthTestService(t)
	stop := startHealthLoop(t, svc, 60, 1)
	defer stop()
	if !pub.waitForCount(1, 5*time.Second) {
		t.Fatal("the first health tick never published")
	}
	svc.spool.countDrop(dropReasonCorrupt)
	changed := time.Now()
	if !pub.waitForCount(2, 5*time.Second) {
		t.Fatal("a changed counter was not published on the health interval")
	}
	if elapsed := time.Since(changed); elapsed > 2500*time.Millisecond {
		t.Errorf("the change took %s to publish on a 1 s health interval", elapsed)
	}
}

// While a command runs the heartbeat ticker republishes it on its own
// cadence, and the clear after it finishes goes out at once.
func TestHealthLoop_HeartbeatRepublishesWhileRunning(t *testing.T) {
	svc, pub := newHealthTestService(t)
	exec := &progressExecutor{}
	svc.Executor = exec
	exec.set(interpreter.Progress{PostID: "hold-1", StartedAt: time.Now()})
	stop := startHealthLoop(t, svc, 1, 60)
	defer stop()
	if !pub.waitForCount(3, 6*time.Second) {
		t.Fatalf("published %d times in 6 s on a 1 s heartbeat", pub.count())
	}
	for i, p := range pub.published {
		if len(p.Commands.InFlight) != 1 || p.Commands.InFlight[0].PostID != "hold-1" {
			t.Errorf("heartbeat %d listed %+v", i, p.Commands.InFlight)
		}
	}
}
