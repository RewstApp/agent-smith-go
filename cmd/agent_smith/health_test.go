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
	err       error
}

func (c *capturingPublisher) publish(_ pahomqtt.Client, props mqtt.ReportedProperties) error {
	c.mu.Lock()
	defer c.mu.Unlock()
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

func newHealthTestService(t *testing.T) (*serviceContext, *capturingPublisher) {
	t.Helper()
	dir := t.TempDir()
	svc := &serviceContext{
		OrgId:         "health-org",
		Executor:      &statsExecutor{stats: interpreter.ExecutionStats{Completed: 7, TimedOut: 2}},
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
	if h.Commands != (mqtt.CommandHealth{Executing: 2, CompletedTotal: 7, TimedOutTotal: 2}) {
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

	if !svc.reportHealth(context.Background(), nil, notifier, logger, true) {
		t.Fatal("the forced first report was not published")
	}
	if svc.reportHealth(context.Background(), nil, notifier, logger, false) {
		t.Error("an unchanged health was published again")
	}
	svc.spool.countDrop(dropReasonExpired)
	if !svc.reportHealth(context.Background(), nil, notifier, logger, false) {
		t.Error("a changed counter was not published")
	}
	if pub.count() != 2 {
		t.Errorf("published %d times, want 2", pub.count())
	}
	if pub.published[1].AgentVersion == "" || pub.published[1].Health.Spool.DroppedExpired != 1 {
		t.Errorf("published %+v", pub.published[1])
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
	if svc.reportHealth(context.Background(), nil, &mockNotifierWrapper{}, logger, true) {
		t.Fatal("a failed publish was reported as published")
	}
	pub.err = nil
	if !svc.reportHealth(context.Background(), nil, &mockNotifierWrapper{}, logger, false) {
		t.Error(
			"after a failed publish the next unchanged check should publish, since nothing was recorded",
		)
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
	if svc.reportHealth(ctx, nil, &mockNotifierWrapper{}, hclog.NewNullLogger(), true) {
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
	if svc.progressSnapshot() != nil {
		t.Fatal("nothing running reported a progress object")
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
	if (&serviceContext{Executor: &funcExecutor{}}).progressSnapshot() != nil {
		t.Error("an executor without progress reported one")
	}
}

func TestReportHealth_PublishesWhileRunningAndClearsAfter(t *testing.T) {
	svc, pub := newHealthTestService(t)
	exec := &progressExecutor{}
	svc.Executor = exec
	ctx := context.Background()
	logger := hclog.NewNullLogger()
	notifier := &mockNotifierWrapper{}
	svc.reportHealth(ctx, nil, notifier, logger, true)

	exec.set(interpreter.Progress{PostID: "hold-1", StartedAt: time.Now()})
	if !svc.reportHealth(ctx, nil, notifier, logger, false) {
		t.Fatal("a running command was not published")
	}
	if !svc.reportHealth(ctx, nil, notifier, logger, false) {
		t.Fatal("a still-running command was not republished (its elapsed time moved)")
	}
	exec.set()
	if !svc.reportHealth(ctx, nil, notifier, logger, false) {
		t.Fatal("the publish that clears the list did not happen")
	}
	if svc.reportHealth(ctx, nil, notifier, logger, false) {
		t.Error("an idle, unchanged state was published again")
	}
	published := pub.published
	if n := len(published); n != 4 {
		t.Fatalf("published %d times, want 4", n)
	}
	if published[0].Commands != nil {
		t.Error("the idle first publish carried a commands object")
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

func TestHealthLoop_KickPublishesAtOnceAndIsSpaced(t *testing.T) {
	svc, pub := newHealthTestService(t)
	exec := &progressExecutor{}
	svc.Executor = exec
	heartbeat, health := 60, 60
	device := agent.Device{
		CommandHeartbeatSeconds:     &heartbeat,
		HealthReportIntervalSeconds: &health,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		svc.healthLoop(ctx, nil, device, &mockNotifierWrapper{}, hclog.NewNullLogger())
		close(done)
	}()
	deadline := time.Now().Add(5 * time.Second)
	kickInstalled := func() bool {
		svc.healthMu.Lock()
		defer svc.healthMu.Unlock()
		return svc.healthKick != nil
	}
	for !kickInstalled() && time.Now().Before(deadline) {
		// sleep-ok: poll until the loop has installed its kick channel
		time.Sleep(10 * time.Millisecond)
	}
	// A command finishing kicks a publish well before the 60 s tick.
	svc.spool.countDrop(dropReasonCorrupt)
	svc.kickHealth()
	for pub.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond) // sleep-ok: poll interval inside a bounded wait
	}
	if pub.count() != 1 {
		t.Fatalf("published %d times after a kick, want 1", pub.count())
	}
	// A second kick inside the spacing window is absorbed.
	svc.spool.countDrop(dropReasonCorrupt)
	svc.kickHealth()
	// sleep-ok: gives an unwanted publish time to happen before asserting it did not
	time.Sleep(200 * time.Millisecond)
	if pub.count() != 1 {
		t.Errorf("a kick within %s of the last publish published again", kickMinSpacing)
	}
	cancel()
	<-done
}
