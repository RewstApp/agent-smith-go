package plugins

import (
	"errors"
	"fmt"
	"io"
	"net/rpc"
	"os/exec"
	"sync"
	"sync/atomic"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/shared"
	"github.com/google/uuid"
	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-plugin"
)

const (
	defaultProtocolVersion = 1
	defaultMagicCookieKey  = "AGENT_SMITH"

	// DefaultHealthCheckInterval is how often the host should call CheckHealth to
	// find notification plugin subprocesses that have exited and relaunch them.
	// Notifications are sparse — a mostly idle agent can go a long time between
	// them — so waiting for the next Notify to discover a dead plugin would leave
	// a wide window with no delivery. The interval is short enough to recover
	// promptly and cheap enough to be free in practice: each check reads an
	// in-process exit flag per plugin and performs no RPC.
	DefaultHealthCheckInterval = 30 * time.Second

	// restartBaseBackoff and restartMaxBackoff bound how often a plugin that keeps
	// dying is relaunched. Without a schedule, a plugin that exits immediately on
	// every launch would be re-spawned on every health check and every
	// notification, turning one broken plugin into a process-spawn loop.
	restartBaseBackoff = 5 * time.Second
	restartMaxBackoff  = 5 * time.Minute

	// restartStableUptime is how long a subprocess must have been running before
	// its death is treated as a one-off rather than a crash loop. A plugin that
	// served notifications for hours and then crashed gets an immediate relaunch;
	// one that dies right after launch stays on the backoff schedule.
	restartStableUptime = 2 * time.Minute
)

var pluginMap = map[string]plugin.Plugin{
	"notifier": &shared.NotifierPlugin{},
}

// NotifierStats holds cumulative, process-wide plugin health counters. They are
// exposed so a silent notification outage — the failure mode this guards against
// — is observable beyond the per-transition log lines. The zero value means
// nothing has gone wrong.
type NotifierStats struct {
	// NotifyFailures counts notifications that could not be delivered to a
	// plugin, whether because the RPC call failed, because it timed out, or
	// because no subprocess was running at the time.
	NotifyFailures int64
	// NotifyTimeouts counts, of NotifyFailures, how many were a plugin accepting
	// a Notify call and never responding within shared.NotifyTimeout (hung, not
	// crashed) rather than an error the plugin returned or a broken RPC channel.
	// Tracked separately so a hang is observable as distinct from a crash.
	NotifyTimeouts int64
	// Restarts counts plugin subprocesses successfully relaunched after an exit.
	Restarts int64
	// RestartFailures counts relaunch attempts that themselves failed.
	RestartFailures int64
}

type NotifierWrapper interface {
	Kill()
	Plugins() []string
	Notify(message string) error
	// CheckHealth relaunches any plugin subprocess that has exited. It is a no-op
	// when every plugin is healthy, when no plugins are configured, or after Kill,
	// and is safe to call concurrently with Notify.
	CheckHealth()
	// Stats returns a snapshot of the cumulative plugin health counters.
	Stats() NotifierStats
}

type optionalNotifierWrapper struct {
	// mu guards client, plugin, closed, failing, startedAt and the restart
	// schedule. Notify is called from every message worker while CheckHealth runs
	// on the host's health monitor goroutine, so the subprocess handle must never
	// be swapped while another caller is using it.
	mu     sync.Mutex
	client *plugin.Client
	plugin shared.Notifier
	name   string

	// info and logWriter are retained so a dead subprocess can be relaunched with
	// exactly the command line and stderr sink it was first started with. info is
	// zero for a wrapper that was not launched from a config entry, which leaves
	// the wrapper a no-op and disables restart.
	info      agent.Plugin
	logWriter io.Writer
	logger    hclog.Logger

	// startedAt is when the current subprocess was launched; zero when none is
	// running. It distinguishes a long-lived plugin's first crash from a crash
	// loop.
	startedAt time.Time

	// nextRestartAt is the earliest time another relaunch may be attempted and
	// restartBackoff is the current delay in the exponential schedule.
	nextRestartAt  time.Time
	restartBackoff time.Duration

	// closed records that Kill has run, so a racing health check can never
	// resurrect a plugin the host has torn down.
	closed bool

	// failing records whether the last delivery attempt failed, so a persistently
	// broken plugin logs once per failure transition instead of once per
	// notification.
	failing bool

	notifyFailures  atomic.Int64
	notifyTimeouts  atomic.Int64
	restarts        atomic.Int64
	restartFailures atomic.Int64
}

// log returns the wrapper's logger, falling back to a no-op logger so a wrapper
// constructed without one never panics.
func (p *optionalNotifierWrapper) log() hclog.Logger {
	if p.logger == nil {
		return hclog.NewNullLogger()
	}
	return p.logger
}

func (p *optionalNotifierWrapper) Kill() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.closed = true
	p.dropLocked()
}

// dropLocked terminates the current subprocess (if any) and clears the handle,
// leaving the wrapper in the "not running" state so the next Notify or
// CheckHealth relaunches it. Killing an already-exited client returns promptly;
// a wedged one can hold the lock for go-plugin's graceful-exit grace period,
// which is bounded and only paid on a path that is already broken.
func (p *optionalNotifierWrapper) dropLocked() {
	if p.client != nil {
		p.client.Kill()
	}

	p.client = nil
	p.plugin = nil
	p.startedAt = time.Time{}
}

func (p *optionalNotifierWrapper) Plugins() []string {
	return []string{p.name}
}

// Notify delivers a notification to the plugin, relaunching a dead subprocess
// first. The RPC call itself is made without the lock held — every message worker
// notifies plugins, and net/rpc is safe for concurrent calls, so serializing them
// would let one slow plugin throttle message processing. Only the handle
// bookkeeping (and any relaunch) is serialized.
func (p *optionalNotifierWrapper) Notify(message string) error {
	p.mu.Lock()

	if p.closed {
		p.mu.Unlock()
		return nil
	}

	// Relaunch before delivering so a notification arriving between the crash and
	// the next health check is still delivered rather than lost.
	if p.deadLocked() {
		p.restartLocked("notify")
	}

	notifier := p.plugin
	client := p.client
	restartable := p.restartable()

	p.mu.Unlock()

	if notifier == nil {
		// A wrapper with no config entry behind it was never launched and stays a
		// silent no-op, preserving the historical behaviour. Otherwise the plugin is
		// configured but not running, which is a real (previously invisible) missed
		// notification.
		if !restartable {
			return nil
		}

		return p.recordFailure(fmt.Errorf("plugin %q is not running", p.name))
	}

	err := notifier.Notify(message)
	if err == nil {
		p.recordSuccess()
		return nil
	}

	timedOut := errors.Is(err, shared.ErrNotifyTimeout)

	// A broken RPC channel means the subprocess is gone; a call that never
	// returned within shared.NotifyTimeout means it is alive but hung. Either way
	// the handle is unusable, so drop it and let the next attempt relaunch —
	// dropLocked kills a still-alive hung subprocess rather than leaving it to
	// wedge every future call. An error the plugin itself returned is a
	// plugin-side failure: it is counted and logged, but the subprocess is
	// working and must be left alone.
	if timedOut || isTransportError(err) {
		p.mu.Lock()
		if timedOut {
			p.notifyTimeouts.Add(1)
		}
		// Only discard the handle this call actually used; a concurrent Notify or
		// health check may already have relaunched the plugin.
		if p.client == client {
			p.dropLocked()
		}
		p.mu.Unlock()
	}

	return p.recordFailure(err)
}

func (p *optionalNotifierWrapper) CheckHealth() {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed || !p.deadLocked() {
		return
	}

	p.restartLocked("health_check")
}

func (p *optionalNotifierWrapper) Stats() NotifierStats {
	return NotifierStats{
		NotifyFailures:  p.notifyFailures.Load(),
		NotifyTimeouts:  p.notifyTimeouts.Load(),
		Restarts:        p.restarts.Load(),
		RestartFailures: p.restartFailures.Load(),
	}
}

// restartable reports whether this wrapper knows how to launch a subprocess. It
// is false only for wrappers not built from a device config plugin entry.
func (p *optionalNotifierWrapper) restartable() bool {
	return p.info.ExecutablePath != ""
}

// deadLocked reports whether the plugin needs relaunching: no handle at all, or
// a subprocess go-plugin has observed exiting. It only ever reports true for a
// restartable wrapper, so a plugin-less wrapper is never treated as broken.
//
// This detects a subprocess that exits or crashes, which is the failure mode
// that silently killed notification delivery. A plugin whose process is alive
// but wedged is not detectable this way; it surfaces instead as a Notify call
// that times out (see shared.NotifyTimeout), which is counted, logged, and
// drops the handle for relaunch exactly like a transport error — dropLocked
// kills the still-alive process rather than leaving it running and unusable.
func (p *optionalNotifierWrapper) deadLocked() bool {
	if !p.restartable() {
		return false
	}

	return p.client == nil || p.plugin == nil || p.client.Exited()
}

// restartLocked relaunches the subprocess, honoring the crash-loop backoff. The
// trigger names what noticed the death and is logged for diagnosis.
func (p *optionalNotifierWrapper) restartLocked(trigger string) {
	if p.closed || !p.restartable() {
		return
	}

	if p.restartBackoff <= 0 {
		p.restartBackoff = restartBaseBackoff
	}

	now := time.Now()

	// A subprocess that stayed up past the stability window earned a clean slate:
	// its death is a one-off, not a crash loop, so relaunch it immediately.
	if !p.startedAt.IsZero() && now.Sub(p.startedAt) >= restartStableUptime {
		p.restartBackoff = restartBaseBackoff
		p.nextRestartAt = time.Time{}
	}

	if now.Before(p.nextRestartAt) {
		p.log().Debug(
			"Notification plugin restart deferred by backoff",
			"plugin", p.name,
			"trigger", trigger,
			"retry_at", p.nextRestartAt,
		)
		return
	}

	// Charge the wait up front so a launch that fails — or that succeeds and then
	// immediately dies again — cannot be retried until the backoff elapses.
	backoff := p.restartBackoff
	p.nextRestartAt = now.Add(backoff)
	p.restartBackoff = min(2*backoff, restartMaxBackoff)

	p.dropLocked()

	client, notifier, err := launchNotifier(p.info, p.logWriter, p.logger)
	if err != nil {
		failures := p.restartFailures.Add(1)
		p.log().Error(
			"Failed to restart notification plugin",
			"plugin", p.name,
			"trigger", trigger,
			"retry_after", backoff,
			"restart_failures", failures,
			"error", err,
		)
		return
	}

	p.client = client
	p.plugin = notifier
	p.startedAt = now

	p.log().Warn(
		"Notification plugin restarted after subprocess exit",
		"plugin", p.name,
		"trigger", trigger,
		"restarts", p.restarts.Add(1),
	)
}

// recordFailure counts a missed notification and logs it once per failure
// transition — the plugin is notified on every status change and every received
// message, so logging each individual failure would flood the log for a plugin
// that stays broken. The error is returned unchanged for the caller to combine.
func (p *optionalNotifierWrapper) recordFailure(err error) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	failures := p.notifyFailures.Add(1)

	if p.failing {
		p.log().Debug(
			"Notification delivery still failing",
			"plugin", p.name,
			"notify_failures", failures,
			"error", err,
		)
		return err
	}

	p.failing = true
	p.log().Error(
		"Notification delivery failed",
		"plugin", p.name,
		"notify_failures", failures,
		"error", err,
	)

	return err
}

// recordSuccess marks a delivered notification. A working plugin proves it is not
// crash looping, so the restart schedule is reset, and a recovery after a failure
// run is logged to close out the failure log line.
func (p *optionalNotifierWrapper) recordSuccess() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.restartBackoff = restartBaseBackoff
	p.nextRestartAt = time.Time{}

	if !p.failing {
		return
	}

	p.failing = false
	p.log().Warn(
		"Notification delivery recovered",
		"plugin", p.name,
		"notify_failures", p.notifyFailures.Load(),
	)
}

type notifierSetWrapper struct {
	notifiers []*optionalNotifierWrapper
}

func (s *notifierSetWrapper) Kill() {
	for _, notifier := range s.notifiers {
		notifier.Kill()
	}
}

func (s *notifierSetWrapper) Plugins() []string {
	names := make([]string, len(s.notifiers))

	for i, notifier := range s.notifiers {
		names[i] = notifier.name
	}

	return names
}

func (s *notifierSetWrapper) Notify(message string) error {
	var combinedErrors error

	for _, notifier := range s.notifiers {
		err := notifier.Notify(message)
		if err != nil {
			combinedErrors = errors.Join(combinedErrors, err)
		}
	}

	return combinedErrors
}

func (s *notifierSetWrapper) CheckHealth() {
	for _, notifier := range s.notifiers {
		notifier.CheckHealth()
	}
}

func (s *notifierSetWrapper) Stats() NotifierStats {
	var stats NotifierStats

	for _, notifier := range s.notifiers {
		pluginStats := notifier.Stats()
		stats.NotifyFailures += pluginStats.NotifyFailures
		stats.NotifyTimeouts += pluginStats.NotifyTimeouts
		stats.Restarts += pluginStats.Restarts
		stats.RestartFailures += pluginStats.RestartFailures
	}

	return stats
}

// pluginLogger returns the logger go-plugin uses for its own diagnostics
// (handshake progress, subprocess exit), named per plugin so those lines are
// attributable in the agent log. A nil host logger yields a no-op logger rather
// than go-plugin's default, which writes to the host process stderr — a place a
// service has no reader for.
func pluginLogger(logger hclog.Logger, name string) hclog.Logger {
	if logger == nil {
		return hclog.NewNullLogger()
	}

	return logger.Named("plugin." + name)
}

// launchNotifier starts the plugin subprocess described by info and dispenses its
// notifier implementation. The magic cookie value is minted per launch, so each
// subprocess — including a relaunch of the same plugin — handshakes with its own
// secret. On any failure the partially started subprocess is killed rather than
// left orphaned, which matters now that launches are retried.
func launchNotifier(
	info agent.Plugin,
	logWriter io.Writer,
	logger hclog.Logger,
) (*plugin.Client, shared.Notifier, error) {
	handshakeConfig := plugin.HandshakeConfig{
		ProtocolVersion:  defaultProtocolVersion,
		MagicCookieKey:   defaultMagicCookieKey,
		MagicCookieValue: uuid.New().String(),
	}

	// #nosec G204
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig: handshakeConfig,
		Plugins:         pluginMap,
		Cmd: exec.Command(
			info.ExecutablePath,
			"--magic-cookie-key",
			handshakeConfig.MagicCookieKey,
			"--magic-cookie-value",
			handshakeConfig.MagicCookieValue,
		),
		Stderr: logWriter,
		Logger: pluginLogger(logger, info.Name),
	})

	rpcClient, err := client.Client()
	if err != nil {
		client.Kill()
		return nil, nil, err
	}

	raw, err := rpcClient.Dispense("notifier")
	if err != nil {
		client.Kill()
		return nil, nil, err
	}

	notifier, ok := toNotifier(raw)
	if !ok {
		client.Kill()
		return nil, nil, fmt.Errorf("plugin %q: Dispense returned unexpected type", info.Name)
	}

	return client, notifier, nil
}

// LoadNotifer launches every configured notification plugin and returns a
// wrapper that fans notifications out to all of them. Plugins that fail to
// launch are reported in the returned error and left out of the set, as before;
// plugins that launch successfully are supervised from then on — see CheckHealth
// and Notify, which relaunch a subprocess that later exits or crashes.
func LoadNotifer(
	plugins []agent.Plugin,
	logWriter io.Writer,
	logger hclog.Logger,
) (NotifierWrapper, error) {
	set := &notifierSetWrapper{}
	var combinedErrors error

	for _, pluginInfo := range plugins {
		client, notifier, err := launchNotifier(pluginInfo, logWriter, logger)
		if err != nil {
			combinedErrors = errors.Join(combinedErrors, err)
			continue
		}

		set.notifiers = append(set.notifiers, &optionalNotifierWrapper{
			client:         client,
			plugin:         notifier,
			name:           pluginInfo.Name,
			info:           pluginInfo,
			logWriter:      logWriter,
			logger:         logger,
			startedAt:      time.Now(),
			restartBackoff: restartBaseBackoff,
		})
	}

	return set, combinedErrors
}

// isTransportError reports whether an error returned by a plugin's Notify call
// means the RPC channel to the subprocess is broken rather than the plugin
// having reported a failure of its own. net/rpc wraps any error returned by the
// remote method in rpc.ServerError; everything else (rpc.ErrShutdown, io.EOF, a
// closed pipe) means the connection — and in practice the subprocess — is gone.
func isTransportError(err error) bool {
	if err == nil {
		return false
	}

	var serverErr rpc.ServerError

	return !errors.As(err, &serverErr)
}

// toNotifier safely asserts raw to shared.Notifier, returning (nil, false) on failure.
func toNotifier(raw interface{}) (shared.Notifier, bool) {
	n, ok := raw.(shared.Notifier)
	return n, ok
}
