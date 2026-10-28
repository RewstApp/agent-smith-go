package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/interpreter"
	"github.com/RewstApp/agent-smith-go/internal/mqtt"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
	"github.com/RewstApp/agent-smith-go/plugins"
	"github.com/hashicorp/go-hclog"
)

// healthFileName is where the service leaves its last fleet-health object in
// the data directory, so --diagnostic can show what the twin was told without
// a channel to the running service.
const healthFileName = "health.json"

// healthSnapshot gathers the fleet-health object (sc-129687) from the counters
// the service already keeps: the journal's pending count and degraded flag,
// the spool's size and drops, the executors' outcomes, the plugins'
// supervision counters and the connection. Every counter is since process
// start.
func (svc *serviceContext) healthSnapshot(notifier plugins.NotifierWrapper) *mqtt.Health {
	now := time.Now()
	h := &mqtt.Health{
		ReportedAt:          now.UTC().Format(time.RFC3339),
		LastConnectionError: svc.connectionError(),
		Plugins:             []mqtt.PluginHealth{},
		Commands: mqtt.CommandHealth{
			Executing: svc.executing.Load(),
		},
	}
	if !svc.startedAt.IsZero() {
		h.UptimeSeconds = int64(now.Sub(svc.startedAt).Seconds())
	}
	if at := svc.connectedAt.Load(); at != 0 {
		h.ConnectedAt = time.Unix(0, at).UTC().Format(time.RFC3339)
	}
	if svc.journal != nil {
		h.Journal = mqtt.JournalHealth{
			Pending:          svc.journal.pendingCount(),
			Degraded:         svc.journalDegraded.Load(),
			ExpiredTotal:     svc.journalExpired.Load(),
			InterruptedTotal: svc.journalInterrupted.Load(),
		}
	}
	if svc.spool != nil {
		h.Spool = svc.spool.health()
	}
	if reporter, ok := svc.Executor.(interpreter.StatsReporter); ok {
		stats := reporter.ExecutionStats()
		h.Commands.CompletedTotal = stats.Completed
		h.Commands.TimedOutTotal = stats.TimedOut
		h.Commands.FailedTotal = stats.Failed
	}
	if notifier != nil {
		for _, p := range notifier.PluginStats() {
			h.Plugins = append(h.Plugins, mqtt.PluginHealth{
				Name:           p.Name,
				Restarts:       p.Restarts,
				NotifyFailures: p.NotifyFailures,
				NotifyTimeouts: p.NotifyTimeouts,
			})
		}
	}
	return h
}

// progressSnapshot gathers the running commands' heartbeats (sc-129689) from
// the executor, when it reports them. Never nil: an empty list is what clears
// the twin's view, including the list a previous process left behind.
func (svc *serviceContext) progressSnapshot() *mqtt.CommandsProgress {
	out := &mqtt.CommandsProgress{InFlight: []mqtt.CommandProgress{}}
	reporter, ok := svc.Executor.(interpreter.ProgressReporter)
	if !ok {
		return out
	}
	now := time.Now()
	for _, p := range reporter.InFlight() {
		out.InFlight = append(out.InFlight, mqtt.CommandProgress{
			PostID:         p.PostID,
			StartedAt:      p.StartedAt.UTC().Format(time.RFC3339),
			ElapsedSeconds: int64(now.Sub(p.StartedAt).Seconds()),
			StdoutBytes:    p.StdoutBytes,
			StderrBytes:    p.StderrBytes,
			LastOutputLine: p.LastOutputLine,
		})
	}
	return out
}

// kickHealth wakes the twin reporter without blocking, for a command that
// just finished: once nothing is running its entry should clear now, not at
// the next tick.
func (svc *serviceContext) kickHealth() {
	svc.healthMu.Lock()
	kick := svc.healthKick
	svc.healthMu.Unlock()
	if kick == nil {
		return
	}
	select {
	case kick <- struct{}{}:
	default:
	}
}

// recordConnectionError keeps the most recent connection-lost reason for the
// health object; an empty string clears it.
func (svc *serviceContext) recordConnectionError(err error) {
	svc.connErrMu.Lock()
	defer svc.connErrMu.Unlock()
	if err == nil {
		svc.lastConnectionError = ""
		return
	}
	svc.lastConnectionError = err.Error()
}

func (svc *serviceContext) connectionError() string {
	svc.connErrMu.Lock()
	defer svc.connErrMu.Unlock()
	return svc.lastConnectionError
}

// reportReason is why the reporter is being asked to publish; it decides what
// has to have changed for a publish to go out.
type reportReason int

const (
	// reportConnected is the publish after a subscribe: everything, always, so
	// the twin reflects this process whatever a previous one left there.
	reportConnected reportReason = iota
	// reportHealthTick is the health interval: publish when the counters
	// differ from the last object published (whichever reason published it).
	reportHealthTick
	// reportHeartbeat is the heartbeat interval: publish while anything runs,
	// since its elapsed time and output counts moved.
	reportHeartbeat
	// reportFinished is a command finishing: publish only once nothing runs
	// and the last publish still listed something, to clear the list at once.
	// A completion while other commands run waits for the heartbeat, so a
	// steady stream of short commands beside a long one cannot publish at the
	// spacing floor.
	reportFinished
)

// reportHealth publishes the current health object and the running commands
// to the device twin when the reason calls for it, and leaves a copy in the
// data directory for --diagnostic when the health or the set of running
// commands changed. A failed publish is a Warn: the twin is informational and
// must never affect command handling. It reports whether it published.
func (svc *serviceContext) reportHealth(
	ctx context.Context,
	client mqtt.Client,
	notifier plugins.NotifierWrapper,
	logger hclog.Logger,
	reason reportReason,
) bool {
	// A cycle that is already ending publishes nothing: the service may be
	// closing its log and the twin is about to be republished by the next
	// connection anyway.
	if ctx.Err() != nil {
		return false
	}
	// The cheap, in-memory checks come first; the health snapshot lists the
	// journal and spool directories under their locks and is taken only when
	// a publish is still possible.
	progress := svc.progressSnapshot()
	inFlight := len(progress.InFlight) > 0
	svc.healthMu.Lock()
	lastInFlight, failed := svc.lastInFlight, svc.publishFailed
	svc.healthMu.Unlock()
	switch reason {
	case reportHeartbeat:
		if !inFlight {
			return false
		}
	case reportFinished:
		// After a failed publish the clear waits for a tick rather than
		// retrying on every completion.
		if inFlight || !lastInFlight || failed {
			return false
		}
	}
	health := svc.healthSnapshot(notifier)
	if reason == reportHealthTick {
		// A list the last publish left behind that no kick has cleared (the
		// clearing publish failed) counts as a change too.
		svc.healthMu.Lock()
		pendingClear := lastInFlight && !inFlight
		same := health.Same(svc.lastHealth) && !pendingClear
		svc.healthMu.Unlock()
		if same {
			return false
		}
	}
	publish := svc.publishHealth
	if publish == nil {
		publish = func(client mqtt.Client, props mqtt.ReportedProperties) error {
			return mqtt.UpdateReportedProperties(client, props, utils.MqttPublishTimeout)
		}
	}
	props := mqtt.ReportedProperties{
		AgentVersion: version.Version,
		Health:       health,
		Commands:     progress,
	}
	err := publish(client, props)
	svc.healthMu.Lock()
	svc.lastAttemptAt = time.Now()
	svc.publishFailed = err != nil
	svc.healthMu.Unlock()
	if err != nil {
		if ctx.Err() == nil {
			logger.Warn("Failed to update device twin fleet health", "error", err)
		}
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	ids := postIDs(progress)
	svc.healthMu.Lock()
	fileChanged := reason == reportConnected || !health.Same(svc.lastHealth) ||
		!slices.Equal(ids, svc.lastFileIDs)
	svc.lastHealth = health
	svc.lastInFlight = inFlight
	if fileChanged {
		svc.lastFileIDs = ids
	}
	svc.healthMu.Unlock()
	// The file is for --diagnostic, which shows the state at the last report;
	// rewriting it (two fsyncs) every heartbeat just to move elapsed_seconds
	// would compete with the journal and spool for the directory for nothing.
	if fileChanged {
		svc.writeHealthFile(&props, logger)
	}
	logger.Debug(
		"Device twin fleet health updated",
		"journal_pending", health.Journal.Pending,
		"journal_degraded", health.Journal.Degraded,
		"spool_entries", health.Spool.Entries,
		"executing", health.Commands.Executing,
		"in_flight", len(progress.InFlight),
	)
	return true
}

// postIDs lists the running commands' post ids, in report order.
func postIDs(p *mqtt.CommandsProgress) []string {
	ids := make([]string, 0, len(p.InFlight))
	for _, c := range p.InFlight {
		ids = append(ids, c.PostID)
	}
	return ids
}

func inFlightOrNone(p *mqtt.CommandsProgress) []mqtt.CommandProgress {
	if p == nil {
		return nil
	}
	return p.InFlight
}

// recentPublish reports whether a publish was attempted within the spacing
// floor, so two ticks that align (the 60 s health tick and the 30 s heartbeat
// do, every minute) do not send two PATCHes a few milliseconds apart.
func (svc *serviceContext) recentPublish() (bool, time.Duration) {
	svc.healthMu.Lock()
	defer svc.healthMu.Unlock()
	since := time.Since(svc.lastAttemptAt)
	return since < kickMinSpacing, kickMinSpacing - since
}

// healthLoop keeps the twin current for the life of the connection cycle: the
// health counters are re-checked every health interval and published when
// they changed, the running commands are republished every heartbeat
// interval while anything runs, and a command finishing publishes the
// clearing update at once. Publishes are spaced at least kickMinSpacing
// apart: a tick inside the floor is skipped (the next one comes), a kick
// inside it is deferred to the end of the floor rather than dropped, so the
// clearing update is coalesced, never lost.
func (svc *serviceContext) healthLoop(
	ctx context.Context,
	client mqtt.Client,
	device agent.Device,
	notifier plugins.NotifierWrapper,
	logger hclog.Logger,
) {
	kick := make(chan struct{}, 1)
	svc.healthMu.Lock()
	svc.healthKick = kick
	svc.healthMu.Unlock()
	defer func() {
		svc.healthMu.Lock()
		svc.healthKick = nil
		svc.healthMu.Unlock()
	}()
	healthTicker := time.NewTicker(device.ResolvedHealthReportInterval())
	defer healthTicker.Stop()
	heartbeatTicker := time.NewTicker(device.ResolvedCommandHeartbeatInterval())
	defer heartbeatTicker.Stop()
	var deferred *time.Timer
	var deferredC <-chan time.Time
	defer func() {
		if deferred != nil {
			deferred.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-healthTicker.C:
			if recent, _ := svc.recentPublish(); !recent {
				svc.reportHealth(ctx, client, notifier, logger, reportHealthTick)
			}
		case <-heartbeatTicker.C:
			if recent, _ := svc.recentPublish(); !recent {
				svc.reportHealth(ctx, client, notifier, logger, reportHeartbeat)
			}
		case <-kick:
			if deferredC != nil {
				// Already waiting; the deferred publish reports the state then.
				continue
			}
			recent, wait := svc.recentPublish()
			if !recent {
				svc.reportHealth(ctx, client, notifier, logger, reportFinished)
				continue
			}
			deferred = time.NewTimer(wait)
			deferredC = deferred.C
		case <-deferredC:
			deferredC = nil
			svc.reportHealth(ctx, client, notifier, logger, reportFinished)
		}
	}
}

// kickMinSpacing is the least time between two twin publishes from this
// reporter, whatever triggered them.
const kickMinSpacing = time.Second

// healthDir is where the health file lives: the org's data directory, or the
// directory a test injected.
func (svc *serviceContext) healthDir() string {
	if svc.healthFileDir != "" {
		return svc.healthFileDir
	}
	return agent.GetDataDirectory(svc.OrgId)
}

// writeHealthFile leaves the health object in the data directory for
// --diagnostic. Best effort: a failure is logged at Debug and never retried.
func (svc *serviceContext) writeHealthFile(props *mqtt.ReportedProperties, logger hclog.Logger) {
	data, err := json.MarshalIndent(props, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(svc.healthDir(), healthFileName)
	if err := utils.WriteFileAtomic(
		utils.NewFileSystem(),
		path,
		data,
		utils.DefaultFileMod,
	); err != nil {
		logger.Debug("Failed to write the fleet-health file", "path", path, "error", err)
	}
}

// readHealthFile loads the reported properties the service last published for
// the org (health and running commands), for --diagnostic.
func readHealthFile(orgId string) (*mqtt.ReportedProperties, error) {
	return readHealthFileFrom(agent.GetDataDirectory(orgId))
}

func readHealthFileFrom(dir string) (*mqtt.ReportedProperties, error) {
	data, err := utils.NewFileSystem().ReadFile(filepath.Join(dir, healthFileName))
	if err != nil {
		return nil, err
	}
	var props mqtt.ReportedProperties
	if err := json.Unmarshal(data, &props); err != nil {
		return nil, err
	}
	if props.Health == nil {
		return nil, errors.New("the fleet-health file carries no health object")
	}
	return &props, nil
}
