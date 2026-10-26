package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
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

// recordConnectionError keeps the most recent connection-lost reason for the
// health object; an empty string clears it.
// progressSnapshot gathers the running commands' heartbeats (sc-129689) from
// the executor, when it reports them. nil when nothing is running.
func (svc *serviceContext) progressSnapshot() *mqtt.CommandsProgress {
	reporter, ok := svc.Executor.(interpreter.ProgressReporter)
	if !ok {
		return nil
	}
	running := reporter.InFlight()
	if len(running) == 0 {
		return nil
	}
	now := time.Now()
	out := &mqtt.CommandsProgress{InFlight: make([]mqtt.CommandProgress, 0, len(running))}
	for _, p := range running {
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
// just finished: its heartbeat entry should clear now, not at the next tick.
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

// reportHealth publishes the current health object to the device twin when it
// differs from the last one published (or when force is set, as on every new
// connection), and leaves a copy in the data directory for --diagnostic. A
// failed publish is a Warn: the twin is informational and must never affect
// command handling.
func (svc *serviceContext) reportHealth(
	ctx context.Context,
	client mqtt.Client,
	notifier plugins.NotifierWrapper,
	logger hclog.Logger,
	force bool,
) bool {
	// A cycle that is already ending publishes nothing: the service may be
	// closing its log and the twin is about to be republished by the next
	// connection anyway.
	if ctx.Err() != nil {
		return false
	}
	health := svc.healthSnapshot(notifier)
	progress := svc.progressSnapshot()
	inFlight := progress != nil
	svc.healthMu.Lock()
	// A running command is a change every time (its elapsed time moved); the
	// publish after the last one ran clears the list; otherwise only a change
	// in the health counters, no more often than the health interval, counts.
	changed := force || inFlight || svc.lastInFlight ||
		(!health.Same(svc.lastHealth) && time.Since(svc.lastPublishedAt) >= svc.healthInterval)
	clearing := !inFlight && svc.lastInFlight
	svc.healthMu.Unlock()
	if !changed {
		return false
	}
	if clearing {
		progress = &mqtt.CommandsProgress{InFlight: []mqtt.CommandProgress{}}
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
	if err := publish(client, props); err != nil {
		if ctx.Err() == nil {
			logger.Warn("Failed to update device twin fleet health", "error", err)
		}
		return false
	}
	if ctx.Err() != nil {
		return false
	}
	svc.healthMu.Lock()
	svc.lastHealth = health
	svc.lastInFlight = inFlight
	svc.lastPublishedAt = time.Now()
	svc.healthMu.Unlock()
	svc.writeHealthFile(&props, logger)
	logger.Debug(
		"Device twin fleet health updated",
		"journal_pending", health.Journal.Pending,
		"journal_degraded", health.Journal.Degraded,
		"spool_entries", health.Spool.Entries,
		"executing", health.Commands.Executing,
		"in_flight", len(inFlightOrNone(progress)),
	)
	return true
}

func inFlightOrNone(p *mqtt.CommandsProgress) []mqtt.CommandProgress {
	if p == nil {
		return nil
	}
	return p.InFlight
}

// healthLoop re-checks the health every interval for the life of the
// connection cycle and publishes when it changed, so a burst of changes
// becomes at most one twin update per interval.
func (svc *serviceContext) healthLoop(
	ctx context.Context,
	client mqtt.Client,
	device agent.Device,
	notifier plugins.NotifierWrapper,
	logger hclog.Logger,
) {
	// The loop ticks at the heartbeat interval so a running command's elapsed
	// time is republished on that cadence; the health counters still coalesce
	// to their own, longer interval inside reportHealth. A kick from a command
	// that just finished publishes at once, spaced by at least a second so a
	// stream of short commands cannot flood the twin.
	kick := make(chan struct{}, 1)
	svc.healthMu.Lock()
	svc.healthInterval = device.ResolvedHealthReportInterval()
	svc.healthKick = kick
	svc.healthMu.Unlock()
	defer func() {
		svc.healthMu.Lock()
		svc.healthKick = nil
		svc.healthMu.Unlock()
	}()
	ticker := time.NewTicker(device.ResolvedCommandHeartbeatInterval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			svc.reportHealth(ctx, client, notifier, logger, false)
		case <-kick:
			svc.healthMu.Lock()
			recent := time.Since(svc.lastPublishedAt) < kickMinSpacing
			svc.healthMu.Unlock()
			if !recent {
				svc.reportHealth(ctx, client, notifier, logger, false)
			}
		}
	}
}

// kickMinSpacing is the least time between two publishes triggered by
// commands finishing; the next tick catches up on anything skipped.
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
