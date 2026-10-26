package main

import (
	"context"
	"encoding/json"
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
	svc.healthMu.Lock()
	unchanged := !force && health.Same(svc.lastHealth)
	svc.healthMu.Unlock()
	if unchanged {
		return false
	}
	publish := svc.publishHealth
	if publish == nil {
		publish = func(client mqtt.Client, props mqtt.ReportedProperties) error {
			return mqtt.UpdateReportedProperties(client, props, utils.MqttPublishTimeout)
		}
	}
	props := mqtt.ReportedProperties{AgentVersion: version.Version, Health: health}
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
	svc.healthMu.Unlock()
	svc.writeHealthFile(health, logger)
	logger.Debug(
		"Device twin fleet health updated",
		"journal_pending", health.Journal.Pending,
		"journal_degraded", health.Journal.Degraded,
		"spool_entries", health.Spool.Entries,
		"executing", health.Commands.Executing,
	)
	return true
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
	interval := device.ResolvedHealthReportInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			svc.reportHealth(ctx, client, notifier, logger, false)
		}
	}
}

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
func (svc *serviceContext) writeHealthFile(health *mqtt.Health, logger hclog.Logger) {
	data, err := json.MarshalIndent(health, "", "  ")
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

// readHealthFile loads the health object the service last published for the
// org, for --diagnostic.
func readHealthFile(orgId string) (*mqtt.Health, error) {
	return readHealthFileFrom(agent.GetDataDirectory(orgId))
}

func readHealthFileFrom(dir string) (*mqtt.Health, error) {
	data, err := utils.NewFileSystem().ReadFile(filepath.Join(dir, healthFileName))
	if err != nil {
		return nil, err
	}
	var h mqtt.Health
	if err := json.Unmarshal(data, &h); err != nil {
		return nil, err
	}
	return &h, nil
}
