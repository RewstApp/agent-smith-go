package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/interpreter"
	"github.com/RewstApp/agent-smith-go/internal/mqtt"
	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/syslog"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
	"github.com/RewstApp/agent-smith-go/plugins"
	"github.com/hashicorp/go-hclog"
)

const (
	// workerCount and messageQueueSize are the defaults applied when the device
	// config does not override them via worker_count / message_queue_size. The
	// effective values are resolved per cycle from the device config; see
	// agent.Device.ResolvedWorkerCount / ResolvedMessageQueueSize.
	workerCount         = agent.DefaultWorkerCount
	messageQueueSize    = agent.DefaultMessageQueueSize
	postbackHTTPTimeout = 30 * time.Second

	// defaultLatestReleaseUrl is the release endpoint the auto-updater queries.
	// Released builds always use it; an integration-test build can be pointed at
	// a stub endpoint instead (see agent.ResolveLatestReleaseUrl).
	defaultLatestReleaseUrl = "https://api.github.com/repos/rewstapp/agent-smith-go/releases/latest"

	postbackMaxAttempts      = agent.DefaultPostbackMaxAttempts
	postbackBaseRetryBackoff = agent.DefaultPostbackBaseRetryBackoff
	postbackMaxRetryBackoff  = agent.DefaultPostbackMaxRetryBackoff

	// maxNotificationPayloadBytes bounds how many bytes of a received message
	// payload are embedded in the AgentReceivedMessage notification forwarded to
	// plugins. Payloads at or below this size are sent verbatim (preserving the
	// existing behaviour for normal-sized messages); larger payloads are
	// summarised so a single oversized workflow message cannot inflate agent
	// memory or overflow the plugin RPC pipe.
	maxNotificationPayloadBytes = 4096
)

type errorResponse struct {
	Error string `json:"error"`
}

func (svc *serviceContext) loadConfig() (agent.Device, error) {
	var device agent.Device

	// Read and parse the config file
	configFileBytes, err := os.ReadFile(svc.ConfigFile)
	if err != nil {
		return device, err
	}

	// Decode the config file
	err = json.Unmarshal(configFileBytes, &device)
	if err != nil {
		return device, err
	}

	if device.MqttQos != nil && *device.MqttQos > 1 {
		return device, fmt.Errorf("mqtt_qos must be 0 or 1; got %d", *device.MqttQos)
	}

	return device, nil
}

// loadLog opens the agent's log file for appending through a size-bounded
// rotating writer. The log used to be a bare append-only *os.File that nothing
// ever rotated, truncated or swept, so on a long-lived endpoint it grew for the
// life of the installation and could fill the system volume - the same volume
// that holds the data directory, the postback spool and the OS. Every other
// file the agent writes there was already bounded; this was the last one that
// was not. See utils.RotatingFile for the rotation and failure semantics, and
// device.ResolvedLogMaxBytes / ResolvedLogMaxFiles for the bounds.
func (svc *serviceContext) loadLog(device agent.Device) (*utils.RotatingFile, error) {
	return utils.NewRotatingFile(
		svc.LogFile,
		int64(device.ResolvedLogMaxBytes()),
		device.ResolvedLogMaxFiles(),
		utils.DefaultFileMod,
		agentLoggerName,
	)
}

// agentLoggerName is the hclog logger name every line in the agent's log file
// carries. The rotating writer stamps its own diagnostics with the same name so
// they parse like the logger's lines; keeping it in one place means the two
// cannot drift apart.
const agentLoggerName = "agent_smith"

// sweepOrgId returns the org id whose directories the startup sweeps reclaim
// files from. The executor and the updater both derive their paths from the
// device config's org id, so the sweeps must use the same value; svc.OrgId (from
// the command line) is only a fallback for a config that omits it.
func (svc *serviceContext) sweepOrgId(device agent.Device) string {
	if device.RewstOrgId != "" {
		return device.RewstOrgId
	}
	return svc.OrgId
}

func (svc *serviceContext) Name() string {
	return agent.GetServiceName(svc.OrgId)
}

func (svc *serviceContext) Execute(
	stop <-chan struct{},
	running chan<- struct{},
) service.ServiceExitCode {
	// Create context to cancel running commands
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Load config
	device, err := svc.loadConfig()
	if err != nil {
		return service.ConfigError
	}

	// Configure the logger
	logFile, err := svc.loadLog(device)
	if err != nil {
		return service.LogFileError
	}
	defer func() {
		_ = logFile.Close()
	}()

	logger := utils.ConfigureLogger(agentLoggerName, logFile, device.LoggingLevel)

	// Migrate the data directory and config file to owner-only permissions for
	// installations that pre-date this hardening (sc-108849), so an endpoint
	// that never goes through install.go/update.go again (e.g. a service that
	// is just restarted) still gets corrected. This runs only now that
	// loadConfig has already proven this process can read svc.ConfigFile, so
	// it never touches permissions on an unvalidated path. Best effort: a
	// failure here must not prevent an otherwise-healthy agent from starting.
	//
	// Deliberately not calling utils.SecureDataDirectoryACL (the Windows ACL
	// lockdown) here: unlike the install/update migration, this runs with the
	// agent's own log file already open for writing (logFile above), and
	// SecureDataDirectoryACL resets ACLs recursively across every file in the
	// directory via icacls /T — including that open one. Relocking an
	// actively-open file's ACL from underneath the same process holding it is
	// what corrupted read/write access to it during integration testing.
	// EnsureSecureFile targets only the config file, which is not held open
	// past loadConfig, so it does not carry that risk.
	dataDir := filepath.Dir(svc.ConfigFile)
	if err := utils.EnsureSecureDir(dataDir); err != nil {
		logger.Warn("Failed to secure data directory", "path", dataDir, "error", err)
	}
	if err := utils.EnsureSecureFile(svc.ConfigFile); err != nil {
		logger.Warn("Failed to secure config file", "path", svc.ConfigFile, "error", err)
	}

	// Configure syslogger if needed
	if device.UseSyslog {
		sysLogger, err := syslog.New(svc.Name(), logFile)
		if err != nil {
			return service.LogFileError
		}
		defer func() {
			err = sysLogger.Close()
			if err != nil {
				logger.Error("Failed to close sys logger handle", "error", err)
			}
		}()

		logger = utils.ConfigureLogger(agentLoggerName, sysLogger, device.LoggingLevel)
	}

	// Resolve the postback retry budget from the device config, falling back to
	// the documented defaults when unset. Existing deployments that omit these
	// keys keep the previous behaviour.
	svc.PostbackMaxAttempts = device.ResolvedPostbackMaxAttempts()
	svc.PostbackBaseRetryBackoff = device.ResolvedPostbackBaseRetryBackoff()

	// Create the durable command journal so an inbound command is persisted
	// before it is acknowledged to the broker. Without it, paho sent the QoS 1
	// PUBACK the instant the subscribe callback returned - i.e. once the payload
	// sat in the in-memory queue - so up to message_queue_size commands could be
	// acknowledged-but-unexecuted at any moment, and a crash, OOM kill or power
	// loss in that window discarded all of them with no redelivery. See
	// commandJournal for why the acknowledgement cannot simply be held until
	// execution finishes.
	svc.journal = newCommandJournal(
		filepath.Join(agent.GetDataDirectory(svc.OrgId), "command_journal"),
		defaultJournalMaxPending,
		defaultJournalMaxAge,
	)

	// Create the durable postback spool so results that exhaust their in-line
	// retry budget are persisted and re-attempted on a later cycle instead of
	// being dropped.
	svc.spool = newPostbackSpool(
		filepath.Join(agent.GetDataDirectory(svc.OrgId), "postback_spool"),
		defaultSpoolMaxEntries,
		defaultSpoolMaxAge,
		defaultSpoolMaxAttempts,
		logger,
	)

	if !device.DisableAutoUpdates {
		updater := agent.NewUpdater(
			logger,
			&device,
			agent.ResolveLatestReleaseUrl(logger, svc.OrgId, defaultLatestReleaseUrl),
			device.GithubToken,
			func(path string, args []string) error {
				// The helper is detached and outlives this process, so it needs a
				// real file descriptor to inherit rather than the rotating writer.
				// A fresh append handle on the active log file gives it one; this
				// process closes its own copy once the child has started, and the
				// child keeps writing through its inherited copy. On Windows that
				// inherited handle blocks a rotation for as long as the helper runs
				// (rotation then degrades to appending and retries), and on
				// Linux/macOS a rotation during the helper's run leaves its output
				// in the rotated copy - both acceptable for a short-lived helper.
				out, err := logFile.OpenAppendHandle()
				if err != nil {
					return err
				}
				defer func() { _ = out.Close() }()
				return detachedCommand(path, args, out, out).Start()
			},
		)
		runner := agent.NewAutoUpdateRunner(
			logger,
			updater,
			agent.DefaultUpdateInterval(),
			agent.DefaultMaxRetries(),
			agent.DefaultBaseBackoff(),
		)
		runner.Start()
		defer runner.Stop()
	}

	// Show header
	logger.Info(
		"Agent Smith started",
		"version",
		version.Version,
		"os",
		runtime.GOOS,
		"device_id",
		device.DeviceId,
		"logging_level",
		device.LoggingLevel,
	)

	defer func() {
		logger.Info("Service stopped")
	}()

	notifier, err := plugins.LoadNotifer(device.Plugins, logFile, logger)
	if err != nil {
		logger.Warn("Failed to load plugin", "error", err)
	}
	defer func() {
		// Notification delivery is best effort at every call site, so a plugin that
		// dies mid-run would otherwise leave no trace at all. Surface the cumulative
		// counters once on the way out whenever anything went wrong.
		if stats := notifier.Stats(); stats != (plugins.NotifierStats{}) {
			logger.Warn(
				"Plugin notification health summary",
				"notify_failures", stats.NotifyFailures,
				"notify_timeouts", stats.NotifyTimeouts,
				"plugin_restarts", stats.Restarts,
				"restart_failures", stats.RestartFailures,
			)
		}

		notifier.Kill()
	}()

	loadedPlugins := notifier.Plugins()
	if len(loadedPlugins) == 1 {
		logger.Info("Plugin loaded", "plugin", loadedPlugins[0])
	} else if len(loadedPlugins) > 1 {
		logger.Info("Plugins loaded", "plugins", loadedPlugins)
	}

	// Create a channel for stopped signal. It is closed (never sent to) when a
	// stop is requested so that closing can never block the monitor goroutine.
	// A closed channel makes every select on <-stopped return immediately and
	// permanently, decoupling teardown timing from the reconnect backoff
	// schedule: a stop arriving while no runCycle is draining stopped (e.g.
	// during the time.After reconnect wait) is still honored at once.
	stopped := make(chan struct{})

	// Monitor the request for the stopped signal
	utils.SafeGo(logger, func() {
		select {
		case <-stop:
			close(stopped)
		case <-ctx.Done():
		}
	}, "scope", "stop_monitor")

	running <- struct{}{}
	_ = notifier.Notify("AgentStarted") // Best effort notification

	// Supervise the plugin subprocesses for the lifetime of the service. A plugin
	// that exits or crashes leaves its RPC client permanently broken, and because
	// every Notify call is best effort the loss of all status notifications would
	// otherwise be completely silent until the agent is restarted. Polling for
	// exits relaunches the plugin proactively instead of waiting for the next
	// notification, which on an idle agent may be a long way off. The monitor is
	// only started when a plugin actually loaded, so a deployment without plugins
	// keeps exactly its previous behaviour. Its stop channel is closed before the
	// deferred notifier.Kill (later defers run first), so the monitor can never
	// resurrect a plugin during teardown.
	if len(loadedPlugins) > 0 {
		monitorStopped := make(chan struct{})
		defer close(monitorStopped)

		utils.SafeGo(logger, func() {
			ticker := time.NewTicker(plugins.DefaultHealthCheckInterval)
			defer ticker.Stop()

			for {
				select {
				case <-monitorStopped:
					return
				case <-ticker.C:
					notifier.CheckHealth()
				}
			}
		}, "scope", "plugin_health_monitor")
	}

	// Reclaim script files left behind by previous runs that were killed before
	// their deferred cleanup could run (force-stop, host power loss, OOM kill).
	// Nothing else ever removes them, so without this sweep they accumulate for
	// the lifetime of the installation. It runs after the service has reported
	// itself running so a slow or crowded scripts directory can never delay
	// startup, and it is best effort by construction — failures are logged inside.
	// The org id is taken from the device config rather than the command line so
	// the swept directory is exactly the one the executor writes to.
	interpreter.SweepStaleScripts(
		agent.GetScriptsDirectory(svc.sweepOrgId(device)),
		interpreter.DefaultStaleScriptAge,
		logger,
	)

	// Reclaim the installer binaries the auto-updater downloaded for previous
	// updates. Download has to keep the file it created so the installer can be
	// executed, and the process that could delete it afterwards is the one the
	// installer replaces, so the only safe moment to remove it is a later start —
	// by which time an installer that is still running is far younger than the
	// age threshold and is left alone. Same placement and best-effort contract as
	// the script sweep above.
	agent.SweepStaleInstallers(
		agent.GetUpdatesDirectory(svc.sweepOrgId(device)),
		agent.DefaultStaleInstallerAge,
		logger,
	)

	// Agents released before the download moved into the org's own updates
	// directory left their installers in the shared system temp directory, where
	// nothing has ever removed them. Sweep that location too so an upgraded
	// endpoint reclaims the binaries it has been accumulating since it was
	// installed, rather than only stopping the growth from here on. The matcher
	// is the same conservative one — this agent's exact temp name pattern,
	// regular files only, a day old at minimum — which is what makes it safe to
	// point at a directory shared with the rest of the system.
	agent.SweepStaleInstallers(os.TempDir(), agent.DefaultStaleInstallerAge, logger)

	rg := utils.ReconnectTimeoutGenerator{}

	for {
		// Wait for the timeout
		if rg.Timeout() > 0 {
			logger.Info("Reconnecting in", "timeout", rg.Timeout())
			select {
			case <-stopped:
				return 0
			case <-time.After(rg.Timeout()):
				logger.Info("Reconnecting...")
				_ = notifier.Notify("AgentStatus:Reconnecting") // Best effort notification
			}
		}

		// Move to the next timeout
		rg.Next()

		shouldReturn, clearBackoff, exitCode := svc.runCycle(ctx, device, logger, notifier, stopped)
		if clearBackoff {
			rg.Clear()
			rg.Next()
		}
		if shouldReturn {
			return exitCode
		}
	}
}

// runCycle runs one MQTT connection attempt through to disconnect. It returns
// (shouldReturn, clearBackoff, exitCode): shouldReturn signals Execute to exit
// with exitCode; clearBackoff signals that a successful connection was
// established and the reconnect backoff should be reset.
//
// A fresh cycleCtx is derived from the parent ctx for each invocation so
// in-flight commands (run via exec.CommandContext) are cancelled when the
// cycle ends. Commands started in a later cycle bind to that cycle's own
// context and are unaffected by the previous cycle's cancellation.
//
// Cleanup is guaranteed on all exit paths. The deferred teardown runs in LIFO
// order: MQTT teardown (Unsubscribe → Disconnect) first so no new messages
// arrive, then cycleCancel to interrupt any hung commands, then close the
// queue and wait for workers. Cancelling before wg.Wait is required —
// otherwise a hung command would block the wait indefinitely. Consolidating
// Unsubscribe and Disconnect in one defer ensures persistent (non-clean)
// Azure IoT Hub sessions don't retain server-side subscriptions across
// reconnects, which would re-deliver buffered messages and cause duplicate
// command execution.
func (svc *serviceContext) runCycle(
	ctx context.Context,
	device agent.Device,
	logger hclog.Logger,
	notifier plugins.NotifierWrapper,
	stopped <-chan struct{},
) (bool, bool, service.ServiceExitCode) {
	cycleCtx, cycleCancel := context.WithCancel(ctx)

	resolvedWorkerCount := device.ResolvedWorkerCount()
	resolvedQueueSize := device.ResolvedMessageQueueSize()

	msgQueue := make(chan inboundMessage, resolvedQueueSize)

	// draining is closed at the very start of teardown (its defer is registered
	// last, so it runs first) to release the subscribe callback if it is blocked
	// applying back-pressure on a full queue. Releasing the callback before the
	// MQTT Unsubscribe/Disconnect is what keeps teardown deadlock-free: paho
	// dispatches messages on a single ordered goroutine, so a permanently
	// blocked callback would also stall the UNSUBACK/disconnect handling.
	draining := make(chan struct{})

	var wg sync.WaitGroup
	for i := range resolvedWorkerCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Debug("Message worker started", "worker", i)
			for {
				select {
				case item, ok := <-msgQueue:
					if !ok {
						logger.Debug("Message worker stopped: queue closed", "worker", i)
						return
					}
					logger.Debug(
						"Message worker processing",
						"worker", i,
						"queue_length", len(msgQueue),
					)
					svc.processInboundGuarded(i, item, cycleCtx, device, logger, notifier)
				case <-cycleCtx.Done():
					logger.Debug("Message worker stopped: context cancelled", "worker", i)
					return
				}
			}
		}()
	}
	defer func() {
		cycleCancel()
		close(msgQueue)
		wg.Wait()
	}()

	// Create a channel to wait for lost connection
	lost := make(chan struct{}, 1)

	opts, err := mqtt.NewClientOptions(device)
	if err != nil {
		logger.Error("Failed to create client options", "error", err)
		return true, false, service.GenericError
	}

	opts.SetAutoReconnect(false)
	// Acknowledge explicitly, after the message has been journaled, rather than
	// letting paho acknowledge on return from the subscribe callback. The
	// difference is what the PUBACK means: with auto-ack it meant "the payload is
	// in an in-memory queue"; now it means "the payload is on disk and will be
	// executed or reported even if this process dies". It also lets a message
	// that could not be accepted at all (journal failed while the cycle was
	// tearing down) stay unacknowledged so the broker redelivers it.
	opts.SetAutoAckDisabled(true)
	opts.OnConnectionLost = func(client mqtt.Client, err error) {
		logger.Error("Connection lost", "error", err)
		lost <- struct{}{}
	}

	topic := fmt.Sprintf("devices/%s/messages/devicebound/#", device.DeviceId)
	qos := byte(1)
	if device.MqttQos != nil {
		qos = *device.MqttQos
	}

	disconnectQuiesce := (uint)(mqtt.DefaultDisconnectQuiesce / time.Millisecond)
	client := mqtt.NewClient(opts)
	subscribed := false
	defer func() {
		if subscribed && client.IsConnected() {
			// Bounded rather than an open-ended Wait: against a black-holed
			// connection (packets dropped with no RST) the UNSUBACK never arrives
			// and an unbounded wait would hold teardown until keepalive plus ping
			// timeout elapsed, blowing the platform's stop deadline. The
			// unsubscribe is only an optimization against persistent sessions, so
			// exceeding the bound warns and falls through to Disconnect.
			token := client.Unsubscribe(topic)
			switch {
			case !token.WaitTimeout(utils.MqttUnsubscribeTimeout):
				logger.Warn(
					"Timed out waiting for unsubscribe; disconnecting anyway",
					"topic", topic,
					"timeout", utils.MqttUnsubscribeTimeout,
				)
			case token.Error() != nil:
				logger.Warn("Failed to unsubscribe", "topic", topic, "error", token.Error())
			}
		}
		client.Disconnect(disconnectQuiesce)
	}()

	// Bound the CONNACK wait as a backstop against a token that never resolves,
	// and make it interruptible so a stop issued mid-handshake is honored at once
	// instead of after the timeout. See utils.MqttConnectWaitMargin for why the
	// bound sits above paho's own ConnectTimeout.
	connectTimeout := device.MqttConnectTimeout() + utils.MqttConnectWaitMargin
	connectStarted := time.Now()
	token := client.Connect()
	switch mqtt.WaitToken(token, connectTimeout, stopped) {
	case mqtt.TokenTimedOut:
		logger.Error(
			"Failed to connect: timed out waiting for broker",
			"timeout", connectTimeout,
			"elapsed", time.Since(connectStarted),
		)
		return false, false, 0
	case mqtt.TokenInterrupted:
		logger.Info("Connect abandoned: service is stopping")
		_ = notifier.Notify("AgentStatus:Stopped") // Best effort notification
		return true, false, 0
	}
	if token.Error() != nil {
		logger.Error("Failed to connect", "error", token.Error())
		return false, false, 0
	}

	err = mqtt.UpdateReportedProperties(client, mqtt.ReportedProperties{
		AgentVersion: version.Version,
	}, utils.MqttPublishTimeout)
	if err != nil {
		logger.Warn("Failed to update device twin reported properties", "error", err)
	} else {
		logger.Info("Device twin reported properties updated", "agent_version", version.Version)
	}

	// Closed first during teardown (registered after the MQTT teardown defer so
	// it runs before it under LIFO) to unblock a back-pressured callback.
	defer close(draining)

	// enqueueMessage applies back-pressure instead of dropping; see its doc for
	// the delivery guarantee and the single (loudly surfaced) teardown drop path.
	token = client.Subscribe(topic, qos, func(client mqtt.Client, msg mqtt.Message) {
		svc.receiveMessage(msg, msgQueue, draining, resolvedQueueSize, logger, notifier)
	})

	// paho puts no deadline on a subscribe token, so a broker that keeps the
	// connection open and answers PINGREQ but never sends SUBACK — a throttling
	// Azure IoT Hub, or a middlebox that half-opens the connection — used to park
	// the cycle here forever: connected, never subscribed, executing nothing, and
	// unable to reach the stop select below. The wait is now bounded and
	// stop-interruptible. A timeout is handled exactly like a subscribe error:
	// the cycle ends and the caller's reconnect backoff (deliberately not
	// cleared) governs the retry, so a throttling broker is backed off from
	// rather than hammered. subscribed stays false on both failure paths, so
	// teardown skips the unsubscribe a broker in this state would not answer
	// either.
	subscribeTimeout := device.MqttSubscribeTimeout()
	subscribeStarted := time.Now()
	switch mqtt.WaitToken(token, subscribeTimeout, stopped) {
	case mqtt.TokenTimedOut:
		logger.Error(
			"Failed to subscribe: timed out waiting for broker acknowledgement",
			"topic", topic,
			"timeout", subscribeTimeout,
			"elapsed", time.Since(subscribeStarted),
		)
		return false, false, 0
	case mqtt.TokenInterrupted:
		logger.Info("Subscribe abandoned: service is stopping", "topic", topic)
		_ = notifier.Notify("AgentStatus:Stopped") // Best effort notification
		return true, false, 0
	}
	if token.Error() != nil {
		logger.Error("Failed to subscribe", "error", token.Error())
		return false, false, 0
	}
	subscribed = true

	logger.Info("Subscribed to messages", "topic", topic, "qos", qos)
	_ = notifier.Notify("AgentStatus:Online") // Best effort notification

	// Now that connectivity is restored, re-attempt any postbacks that were
	// spooled to disk when the engine was previously unreachable. Run it on a
	// cycle-scoped goroutine so it cannot block the connection loop or teardown.
	utils.SafeGo(logger, func() {
		svc.flushPostbackSpool(cycleCtx, device, logger, notifier)
	}, "scope", "postback_spool_flush")

	// Replay commands a previous run accepted from the broker but never
	// finished - the acknowledged-but-unexecuted window this journal closes.
	// Cycle-scoped like the spool flush so it can neither block the connection
	// loop nor delay teardown; a replay that is still feeding the queue when the
	// cycle ends leaves the remaining entries journaled for the next one.
	utils.SafeGo(logger, func() {
		svc.replayJournal(cycleCtx, msgQueue, draining, resolvedQueueSize, device, logger, notifier)
	}, "scope", "command_journal_replay")

	// Proactively renew the SAS token before Azure IoT Hub expires it. The token
	// minted for this connection is valid for device.SasTokenLifetime(); Azure
	// tears down the MQTT connection the instant it expires, which would surface
	// as an Error-level "Connection lost" (indistinguishable from a real fault)
	// and a forced reconnect on a fixed cadence. Instead we end this cycle
	// gracefully a safety margin ahead of expiry so the next cycle mints a fresh
	// token — keeping the connection effectively continuous and reserving the
	// "Connection lost" log for genuine faults. Like the lost-connection path it
	// clears the reconnect backoff so the fresh cycle starts promptly.
	tokenLifetime := device.SasTokenLifetime()
	renewAfter := tokenLifetime - utils.SasTokenRenewMargin(tokenLifetime)
	renewTimer := time.NewTimer(renewAfter)
	defer renewTimer.Stop()

	select {
	case <-stopped:
		_ = notifier.Notify("AgentStatus:Stopped") // Best effort notification
		return true, true, 0
	case <-lost:
		_ = notifier.Notify("AgentStatus:Offline") // Best effort notification
		return false, true, 0
	case <-renewTimer.C:
		logger.Info(
			"Renewing SAS token before expiry",
			"token_lifetime", tokenLifetime,
			"renew_after", renewAfter,
		)
		_ = notifier.Notify("AgentStatus:Reconnecting") // Best effort notification
		return false, true, 0
	}
}

// buildReceivedMessageNotification builds the bounded "AgentReceivedMessage"
// notification string forwarded to plugins for a received payload. Payloads at
// or below maxNotificationPayloadBytes are embedded verbatim. Larger payloads
// are summarised as their total byte length plus a truncated prefix, so the
// resulting notification stays a fixed maximum size regardless of payload size —
// avoiding extra full-payload copies and the risk of overflowing the plugin RPC
// pipe.
func buildReceivedMessageNotification(payload []byte) string {
	const prefix = "AgentReceivedMessage:"

	if len(payload) <= maxNotificationPayloadBytes {
		return prefix + string(payload)
	}

	return fmt.Sprintf(
		"%s[truncated %d bytes] %s",
		prefix,
		len(payload),
		payload[:maxNotificationPayloadBytes],
	)
}

// inboundMessage is one command travelling from the subscribe callback to a
// worker. Key is its command-journal key, or empty when the message could not
// be journaled and is being carried in memory only.
type inboundMessage struct {
	Payload []byte
	Key     string
}

// receiveMessage is the subscribe callback: journal the payload, hand it to a
// worker, then acknowledge it to the broker.
//
// The order is the point. Azure IoT Hub redelivers a QoS 1 message it has not
// received a PUBACK for after a fixed one-minute lock, forever, without
// counting toward the delivery limit - so the acknowledgement cannot wait for
// execution (the default command timeout is thirty minutes). It also must not
// be sent before the command is safe: the auto-ack this replaced fired the
// moment the payload reached the in-memory queue, which is how a crash with a
// full queue lost every queued command. Writing the entry first makes the
// acknowledgement mean "durably accepted": if this process dies, the next
// start replays the entry.
//
// Outcomes:
//   - journaled and enqueued: acknowledged.
//   - journaled but the cycle is tearing down: acknowledged; the entry is
//     replayed on the next cycle. This used to be a drop.
//   - already journaled or completed (a redelivery, e.g. the broker resending
//     on reconnect a message whose PUBACK was lost): acknowledged, not enqueued.
//   - journal write failed: accepted in memory and acknowledged anyway, as
//     before this journal existed - a disk problem must not stop commands from
//     running - with the failure reported once per transition. If the cycle is
//     also tearing down there is nowhere to put it: it is left unacknowledged,
//     so the broker redelivers it, and that is counted.
func (svc *serviceContext) receiveMessage(
	msg mqtt.Message,
	msgQueue chan<- inboundMessage,
	draining <-chan struct{},
	queueSize int,
	logger hclog.Logger,
	notifier plugins.NotifierWrapper,
) {
	payload := msg.Payload()
	item := inboundMessage{Payload: payload}

	if svc.journal != nil {
		key := journalKey(payload)
		existed, err := svc.journal.put(key, payload)
		switch {
		case err != nil:
			svc.recordJournalOutcome(err, logger, notifier)
		case existed:
			logger.Info(
				"Duplicate delivery acknowledged without execution",
				"key", key,
				"broker_dup_flag", msg.Duplicate(),
			)
			msg.Ack()
			return
		default:
			svc.recordJournalOutcome(nil, logger, notifier)
			item.Key = key
		}
	}

	enqueued := svc.enqueueMessage(item, msgQueue, draining, queueSize, logger, notifier)
	if enqueued || item.Key != "" {
		msg.Ack()
	}
}

// recordJournalOutcome folds one journal write result into a once-per-
// transition report, the same "counted, not fatal" treatment the syslog
// forwarder and the log rotator give their own failures.
func (svc *serviceContext) recordJournalOutcome(
	err error,
	logger hclog.Logger,
	notifier plugins.NotifierWrapper,
) {
	if err == nil {
		if svc.journalDegraded.CompareAndSwap(true, false) {
			logger.Info("Command journal recovered; inbound commands are durable again")
		}
		return
	}
	if svc.journalDegraded.CompareAndSwap(false, true) {
		logger.Error(
			"Command journal unavailable; inbound commands are accepted in memory only "+
				"and a crash before they finish would lose them",
			"error", err,
		)
		_ = notifier.Notify("AgentCommandJournalDegraded") // Best effort notification
	}
}

// enqueueMessage hands a received command to the worker queue, applying
// back-pressure rather than dropping. The subscribe callback (and thus this
// function) runs on paho's single ordered dispatch goroutine, and the caller
// acknowledges only after this returns, so blocking here stops the broker from
// considering the message delivered: when the queue is full the call waits until
// a worker frees a slot, and if the agent stays saturated paho's inbound buffer
// fills so the broker holds later messages instead of the agent discarding them.
//
// The escape is a command arriving while the cycle is tearing down (draining
// closed), selected so teardown can never deadlock on a full queue. A journaled
// command is not lost by it - the entry is replayed on the next cycle - and the
// caller still acknowledges it. Only a command that could not be journaled is
// lost from this process's point of view; it is left unacknowledged, which is
// what actually makes the broker redeliver it, and that case is surfaced
// loudly: an Error log, a cumulative counter, and a best-effort plugin
// notification.
//
// Returns true when the command was enqueued, false when it was not.
func (svc *serviceContext) enqueueMessage(
	item inboundMessage,
	msgQueue chan<- inboundMessage,
	draining <-chan struct{},
	queueSize int,
	logger hclog.Logger,
	notifier plugins.NotifierWrapper,
) bool {
	select {
	case msgQueue <- item:
		return true
	case <-draining:
		if item.Key != "" {
			logger.Info(
				"Message received during shutdown; journaled for replay on the next connection",
				"key", item.Key,
			)
			return false
		}
		dropped := svc.droppedMessages.Add(1)
		logger.Error(
			"Message not accepted: received during shutdown and could not be journaled; "+
				"left unacknowledged so the broker redelivers it after its lock expires",
			"queue_size", queueSize,
			"unaccepted_total", dropped,
		)
		_ = notifier.Notify(
			fmt.Sprintf("AgentMessageDropped:shutdown (dropped_total=%d)", dropped),
		) // Best effort notification
		return false
	}
}

// processInboundGuarded runs processInbound with per-message panic recovery.
// Because the payload is untrusted (received over MQTT), a malformed message,
// an unexpected nil, a plugin RPC fault, or any library panic on this path must
// not crash the process. Recovering per-message — rather than per-worker-loop —
// contains the fault to the single offending message and keeps the worker alive
// to process the next item, so the pool stays at full strength. The recovered
// value and a stack trace are logged at Error level (with the worker id) to aid
// diagnosis. Normal error returns are unaffected.
func (svc *serviceContext) processInboundGuarded(
	workerId int,
	item inboundMessage,
	ctx context.Context,
	device agent.Device,
	logger hclog.Logger,
	notifier plugins.NotifierWrapper,
) {
	defer utils.Recover(logger, "worker", workerId, "scope", "processMessage")
	svc.processInbound(item, ctx, device, logger, notifier)
}

// processInbound brackets processMessage with the journal's lifecycle: the
// entry is marked started before execution begins, so a replay after a crash
// knows not to run the command a second time, and completed once the result
// has settled - delivered, spooled, or terminally dropped. An outcome that did
// not settle (the cycle was cancelled mid-way) leaves the entry as it is, and
// the next cycle's replay reports it as interrupted.
func (svc *serviceContext) processInbound(
	item inboundMessage,
	ctx context.Context,
	device agent.Device,
	logger hclog.Logger,
	notifier plugins.NotifierWrapper,
) {
	journaled := item.Key != "" && svc.journal != nil
	if journaled {
		if err := svc.journal.markStarted(item.Key); err != nil {
			logger.Warn(
				"Failed to mark journaled command as started",
				"key", item.Key,
				"error", err,
			)
		}
	}

	settled := svc.processMessage(item.Payload, ctx, device, logger, notifier)

	if journaled && settled {
		if err := svc.journal.complete(item.Key); err != nil {
			logger.Warn("Failed to complete journaled command", "key", item.Key, "error", err)
		}
	}
}

// processMessage parses, executes and posts back one command. It reports
// whether the command's outcome settled: true when the result was delivered,
// spooled, terminally dropped, or there was nothing to post back; false only
// when the cycle was cancelled before the outcome was known, in which case a
// journaled command is reported as interrupted on the next cycle rather than
// silently lost.
func (svc *serviceContext) processMessage(
	payload []byte,
	ctx context.Context,
	device agent.Device,
	logger hclog.Logger,
	notifier plugins.NotifierWrapper,
) bool {
	var message interpreter.Message
	err := message.Parse(payload)
	if err != nil {
		logger.Error("Parse failed", "error", err)
		// A payload that does not parse will not parse on replay either.
		return true
	}

	_ = notifier.Notify(
		buildReceivedMessageNotification(payload),
	) // Best effort notification

	// Execute the message
	resultBytes := message.Execute(
		svc.Executor,
		ctx,
		device,
		logger,
		svc.Sys,
		svc.Domain,
	)

	// A command cut short by the cycle ending did not produce a result the
	// engine should trust; leave it for the replay to report.
	if ctx.Err() != nil {
		return false
	}

	// Skip if there is no post_id specified
	if message.PostId == "" {
		return true
	}

	// Skip postback if disabled in config (ignored when executor always posts back)
	if device.DisableAgentPostback && !svc.Executor.AlwaysPostback() {
		return true
	}

	return svc.sendPostbackWithRetry(ctx, &message, device, resultBytes, logger, notifier)
}

// replayJournal feeds the commands a previous run left in the journal back
// through the worker queue. Entries that had not started execute exactly as
// they would have; entries that had started are reported to the engine as
// interrupted rather than run again, because a partial first run of a
// non-idempotent script followed by a second full run is the failure mode
// at-least-once delivery is most often criticised for; entries older than the
// journal's max age - the broker would have expired them too - are reported
// and discarded.
func (svc *serviceContext) replayJournal(
	ctx context.Context,
	msgQueue chan<- inboundMessage,
	draining <-chan struct{},
	queueSize int,
	device agent.Device,
	logger hclog.Logger,
	notifier plugins.NotifierWrapper,
) {
	if svc.journal == nil {
		return
	}
	fresh, expired, err := svc.journal.pending()
	if err != nil {
		logger.Error("Failed to read the command journal for replay", "error", err)
		return
	}
	if len(fresh) == 0 && len(expired) == 0 {
		return
	}

	for _, e := range expired {
		logger.Error(
			"Journaled command expired before it could run; discarding it",
			"key", e.Key,
			"received_at", e.ReceivedAt,
			"max_age", svc.journal.maxAge,
		)
		_ = notifier.Notify("AgentCommandExpired:" + e.Key) // Best effort notification
		if err := svc.journal.discard(e.Key); err != nil {
			logger.Warn("Failed to discard expired journal entry", "key", e.Key, "error", err)
		}
	}

	executed, interrupted := 0, 0
	for _, e := range fresh {
		if ctx.Err() != nil {
			break
		}
		if !e.StartedAt.IsZero() {
			svc.reportInterrupted(ctx, e, device, logger, notifier)
			interrupted++
			continue
		}
		logger.Info(
			"Replaying command a previous run accepted but never executed",
			"key", e.Key,
			"received_at", e.ReceivedAt,
		)
		item := inboundMessage{Payload: e.Payload, Key: e.Key}
		if !svc.enqueueMessage(item, msgQueue, draining, queueSize, logger, notifier) {
			break // tearing down; the rest stay journaled for the next cycle
		}
		executed++
	}

	logger.Info(
		"Command journal replayed",
		"executed", executed,
		"reported_interrupted", interrupted,
		"expired", len(expired),
	)
}

// reportInterrupted tells the engine that a command started and did not
// finish, then completes the journal entry so it is not reported again.
func (svc *serviceContext) reportInterrupted(
	ctx context.Context,
	e journalEntry,
	device agent.Device,
	logger hclog.Logger,
	notifier plugins.NotifierWrapper,
) {
	var message interpreter.Message
	canReport := message.Parse(e.Payload) == nil &&
		message.PostId != "" &&
		(!device.DisableAgentPostback || svc.Executor.AlwaysPostback())

	logger.Error(
		"Command was interrupted by an agent stop after it had started; "+
			"reporting it to the engine instead of running it again",
		"key", e.Key,
		"post_id", message.PostId,
		"received_at", e.ReceivedAt,
		"started_at", e.StartedAt,
		"reported", canReport,
	)
	_ = notifier.Notify("AgentCommandInterrupted:" + e.Key) // Best effort notification

	settled := true
	if canReport {
		reason := fmt.Sprintf(
			"the agent stopped after starting this command at %s (received %s) and did not finish it",
			e.StartedAt.UTC().Format(time.RFC3339),
			e.ReceivedAt.UTC().Format(time.RFC3339),
		)
		result := interpreter.InterruptedResultBytes(logger, reason)
		settled = svc.sendPostbackWithRetry(ctx, &message, device, result, logger, notifier)
	}
	if settled {
		if err := svc.journal.complete(e.Key); err != nil {
			logger.Warn("Failed to complete interrupted journal entry", "key", e.Key, "error", err)
		}
	}
}

// postbackRetryBackoff computes the delay to wait before the given postback
// retry attempt. attempt is 1-based and counts the initial try, so the first
// backoff is paid before attempt 2; the uncapped schedule is base * 2^(attempt-2).
//
// The schedule is computed by utils.JitteredBackoff, which performs the doubling
// by iterated multiplication with an early exit at maxBackoff rather than the
// naive base * (1 << (attempt-2)) shift. That shift grows without bound and, for
// a large operator-configured postback_max_attempts, overflows int64 nanoseconds
// into a negative time.Duration — time.After then fires immediately and the retry
// loop busy-spins. Clamping to maxBackoff before any overflow can occur keeps
// every slot strictly positive and bounded, so raising the attempt count widens
// the total retry window without ever producing a negative, zero, or multi-day
// sleep. Up to ±25% jitter is applied to avoid synchronized retry storms across
// many agents. The same helper backs the auto-update retry schedule (see
// agent.DefaultUpdateMaxRetryBackoff).
func postbackRetryBackoff(base, maxBackoff time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = postbackBaseRetryBackoff
	}
	if maxBackoff <= 0 {
		maxBackoff = postbackMaxRetryBackoff
	}

	return utils.JitteredBackoff(base, maxBackoff, attempt-2)
}

// sendPostbackWithRetry posts the command result to the Rewst engine, retrying
// transient failures (network errors and 5xx responses) with exponential
// backoff. Non-retryable responses (2xx success, 400 "already fulfilled", and
// other 4xx errors) terminate the loop immediately. When all in-line attempts
// fail the result is not silently dropped: the failure is surfaced via a
// best-effort AgentPostbackFailed plugin notification and the result is spooled
// to disk for re-attempt on a later cycle.
func (svc *serviceContext) sendPostbackWithRetry(
	ctx context.Context,
	message *interpreter.Message,
	device agent.Device,
	resultBytes []byte,
	logger hclog.Logger,
	notifier plugins.NotifierWrapper,
) bool {
	maxAttempts := svc.PostbackMaxAttempts
	if maxAttempts < 1 {
		maxAttempts = postbackMaxAttempts
	}
	baseBackoff := svc.PostbackBaseRetryBackoff
	if baseBackoff <= 0 {
		baseBackoff = postbackBaseRetryBackoff
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if attempt > 1 {
			backoff := postbackRetryBackoff(baseBackoff, postbackMaxRetryBackoff, attempt)
			logger.Info(
				"Retrying postback",
				"post_id", message.PostId,
				"attempt", attempt,
				"max_attempts", maxAttempts,
				"backoff", backoff,
			)
			select {
			case <-ctx.Done():
				logger.Error(
					"Postback aborted before retry: context cancelled",
					"post_id", message.PostId,
					"attempts", attempt-1,
					"error", ctx.Err(),
				)
				return false
			case <-time.After(backoff):
			}
		}

		// Both failure outcomes retry in-line: whether the engine was unreachable
		// or rejected the request, another attempt is the right response while the
		// budget lasts. The distinction only matters to the spool flush, which has
		// other entries to get to.
		outcome, err := svc.attemptPostback(ctx, message, device, resultBytes, logger, attempt)
		if outcome == deliveryDone {
			return true
		}
		lastErr = err
	}

	// A budget exhausted only because the cycle was cancelled mid-way has not
	// settled anything; the journal will report the command as interrupted.
	if ctx.Err() != nil {
		return false
	}

	// All in-line attempts failed. Surface the failure beyond the log and, when a
	// spool is configured, persist the result for re-attempt on a later cycle so
	// a transient engine outage does not silently lose it.
	logger.Error(
		"Postback failed: all in-line retries exhausted",
		"post_id", message.PostId,
		"attempts", maxAttempts,
		"last_error", lastErr,
	)
	_ = notifier.Notify(
		fmt.Sprintf("AgentPostbackFailed:%s", message.PostId),
	) // Best effort notification

	if svc.spool == nil {
		logger.Error("Postback result dropped: no spool configured", "post_id", message.PostId)
		return true
	}

	if err := svc.spool.enqueue(spoolEntry{
		PostId:    message.PostId,
		Result:    resultBytes,
		CreatedAt: time.Now(),
	}); err != nil {
		logger.Error(
			"Postback result dropped: failed to spool for later delivery",
			"post_id", message.PostId,
			"error", err,
		)
		return true
	}

	logger.Warn(
		"Postback result spooled for later delivery",
		"post_id", message.PostId,
	)
	return true
}

// flushPostbackSpool re-attempts delivery of any command results whose in-line
// postback previously exhausted its retry budget and was spooled to disk. Each
// entry is given a single attempt per cycle: a success or permanent (4xx)
// rejection removes it from the spool, an unreachable engine leaves it (and the
// rest) for a later cycle, and an entry the engine rejects is passed over so the
// entries behind it still get their attempt. It is bound to the cycle context so
// it neither blocks the connection loop nor delays teardown — cycle cancellation
// aborts any in-flight request and stops the flush between entries.
//
// Abandoning an entry that exhausted its attempts is surfaced the same way an
// exhausted in-line retry budget is: a best-effort plugin notification, so the
// loss is visible outside the log file.
func (svc *serviceContext) flushPostbackSpool(
	ctx context.Context,
	device agent.Device,
	logger hclog.Logger,
	notifier plugins.NotifierWrapper,
) {
	if svc.spool == nil {
		return
	}
	svc.spool.flush(
		ctx,
		func(entry spoolEntry) (deliveryOutcome, error) {
			msg := &interpreter.Message{PostId: entry.PostId}
			return svc.attemptPostback(ctx, msg, device, entry.Result, logger, 1)
		},
		func(entry spoolEntry, err error) {
			logger.Error(
				"Postback result abandoned: engine kept rejecting it",
				"post_id", entry.PostId,
				"attempts", entry.Attempts,
				"last_error", entry.LastError,
			)
			if notifier != nil {
				_ = notifier.Notify(
					fmt.Sprintf("AgentPostbackAbandoned:%s", entry.PostId),
				) // Best effort notification
			}
		},
	)
}

// attemptPostback performs a single postback attempt and classifies the result
// (see deliveryOutcome). deliveryDone means no further attempt should be made —
// success, "already fulfilled", or a non-retryable 4xx. deliveryRetryEntry means
// the engine answered but would not take this request (5xx, or a body that could
// not be parsed). deliveryUnreachable means no answer arrived at all: a
// transport error, or a connection that broke while the response was being read.
// The returned error describes the failure for the caller's summary log.
//
// The reachable/unreachable split is what lets the spool flush tell an engine
// outage apart from one entry the engine refuses; do not collapse the two
// failure outcomes back into a single boolean.
func (svc *serviceContext) attemptPostback(
	ctx context.Context,
	message *interpreter.Message,
	device agent.Device,
	resultBytes []byte,
	logger hclog.Logger,
	attempt int,
) (deliveryOutcome, error) {
	postbackReq, err := message.CreatePostbackRequest(
		ctx,
		device,
		bytes.NewReader(resultBytes),
	)
	if err != nil {
		logger.Error(
			"Failed to create postback request",
			"post_id", message.PostId,
			"attempt", attempt,
			"error", err,
		)
		// Local and permanent: a request this message cannot build will not build
		// on a later cycle either.
		return deliveryDone, err
	}

	if attempt == 1 {
		logger.Info("Sending postback", "post_id", message.PostId, "url", postbackReq.URL)
	}

	res, err := svc.HTTPClient.Do(postbackReq)
	if err != nil {
		logger.Error(
			"Failed to send postback",
			"post_id", message.PostId,
			"attempt", attempt,
			"error", err,
		)
		// No answer at all — the engine is unreachable, not refusing this entry.
		return deliveryUnreachable, err
	}
	defer func() {
		if cerr := res.Body.Close(); cerr != nil {
			logger.Error("Failed to close response", "error", cerr)
		}
	}()

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		logger.Error(
			"Failed to read postback response body",
			"post_id", message.PostId,
			"attempt", attempt,
			"error", err,
		)
		// The connection broke mid-response: the engine answered but the exchange
		// did not complete, so treat it as connectivity rather than rejection.
		return deliveryUnreachable, err
	}

	if res.StatusCode == http.StatusOK {
		logger.Info("Postback sent", "post_id", message.PostId, "attempt", attempt)
		if len(bodyBytes) > 0 {
			logger.Info("Received response", "data", string(bodyBytes))
		}
		return deliveryDone, nil
	}

	var response errorResponse
	parseErr := json.Unmarshal(bodyBytes, &response)

	if parseErr == nil && res.StatusCode == http.StatusBadRequest &&
		strings.Contains(strings.ToLower(response.Error), "fulfilled") {
		logger.Info("Postback already sent", "post_id", message.PostId)
		return deliveryDone, nil
	}

	// 5xx responses (and any other unexpected non-2xx without a parseable body)
	// are treated as transient. 4xx responses with a parseable error body are
	// terminal — retrying a malformed request will not help.
	//
	// Transient is not the same as unreachable: the engine answered, so it is
	// plainly up and every other spooled entry may still be deliverable. This is
	// the case that used to be misread as an outage and stall the whole spool.
	retryable := res.StatusCode >= 500 || parseErr != nil

	if retryable {
		logger.Error(
			"Postback failed (will retry if attempts remain)",
			"post_id", message.PostId,
			"attempt", attempt,
			"status_code", res.StatusCode,
			"message", response.Error,
		)
		if parseErr != nil && len(bodyBytes) > 0 {
			logger.Error("Received error response", "data", string(bodyBytes))
		}
		return deliveryRetryEntry, fmt.Errorf(
			"postback failed: status %d: %s",
			res.StatusCode,
			response.Error,
		)
	}

	logger.Error(
		"Postback failed (non-retryable)",
		"post_id", message.PostId,
		"attempt", attempt,
		"status_code", res.StatusCode,
		"message", response.Error,
	)
	return deliveryDone, fmt.Errorf(
		"postback failed: status %d: %s",
		res.StatusCode,
		response.Error,
	)
}

func runService(params *serviceContext) {
	exitCode, _ := service.Run(params)
	os.Exit(exitCode)
}
