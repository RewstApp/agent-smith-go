package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/mqtt"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
	"github.com/hashicorp/go-hclog"
)

// Runtime settings from the device twin (sc-129688). Rewst sets a desired
// property on the agent's twin; the agent validates it with the same rules as
// the matching CLI flag, applies it live where the component allows it and at
// the next safe point where it does not, rewrites the config file so a restart
// keeps it, and acknowledges every key under reported.settings_ack.

// Acknowledgement statuses; see mqtt.SettingAck.
const (
	settingApplied     = "applied"
	settingRejected    = "rejected"
	settingUnsupported = "unsupported"
	settingCleared     = "cleared"
)

// maxUnsupportedAcks bounds how many unknown keys one desired document can
// make the agent echo back, so a document full of junk cannot push the
// reported properties past IoT Hub's size limit.
const maxUnsupportedAcks = 32

// twinGetTimeout is how long the agent waits for the twin GET it sends on each
// connection before giving up on it for the cycle; PATCHes still apply.
const twinGetTimeout = 30 * time.Second

// runtimeSetting is one key on the allow-list: how to parse a desired value
// (validated like the CLI flag), read the value in effect, and set it.
type runtimeSetting struct {
	parse func(raw json.RawMessage) (any, error)
	get   func(d agent.Device) any
	set   func(d *agent.Device, v any)
}

// runtimeSettings is the allow-list. The keys are the config file's own keys,
// so the file is rewritten under the same names.
var runtimeSettings = map[string]runtimeSetting{
	"logging_level": {
		parse: parseLoggingLevel,
		get: func(d agent.Device) any {
			if d.LoggingLevel == utils.Default {
				return string(utils.Info)
			}
			return string(d.LoggingLevel)
		},
		set: func(d *agent.Device, v any) { d.LoggingLevel = utils.LoggingLevel(v.(string)) },
	},
	"command_timeout_seconds": intSetting(
		"command-timeout-seconds",
		func(d agent.Device) int { return int(d.ResolvedCommandTimeout() / time.Second) },
		func(d *agent.Device, v *int) { d.CommandTimeoutSeconds = v },
	),
	"worker_count": intSetting(
		"worker-count",
		func(d agent.Device) int { return d.ResolvedWorkerCount() },
		func(d *agent.Device, v *int) { d.WorkerCount = v },
	),
	"max_output_bytes": intSetting(
		"max-output-bytes",
		func(d agent.Device) int { return d.ResolvedMaxOutputBytes() },
		func(d *agent.Device, v *int) { d.MaxOutputBytes = v },
	),
	"postback_max_attempts": intSetting(
		"postback-max-attempts",
		func(d agent.Device) int { return d.ResolvedPostbackMaxAttempts() },
		func(d *agent.Device, v *int) { d.PostbackMaxAttempts = v },
	),
	"postback_base_retry_backoff_seconds": intSetting(
		"postback-base-retry-backoff-seconds",
		func(d agent.Device) int { return int(d.ResolvedPostbackBaseRetryBackoff() / time.Second) },
		func(d *agent.Device, v *int) { d.PostbackBaseRetryBackoffSeconds = v },
	),
	"disable_auto_updates": {
		parse: func(raw json.RawMessage) (any, error) {
			var b bool
			if err := json.Unmarshal(raw, &b); err != nil {
				return nil, errors.New("invalid disable_auto_updates: must be true or false")
			}
			return b, nil
		},
		get: func(d agent.Device) any { return d.DisableAutoUpdates },
		set: func(d *agent.Device, v any) { d.DisableAutoUpdates = v.(bool) },
	},
}

// intSetting is a positive-integer setting validated like its CLI flag.
func intSetting(
	flag string,
	get func(agent.Device) int,
	set func(*agent.Device, *int),
) runtimeSetting {
	return runtimeSetting{
		parse: func(raw json.RawMessage) (any, error) { return parsePositiveSetting(flag, raw) },
		get:   func(d agent.Device) any { return get(d) },
		set: func(d *agent.Device, v any) {
			n := v.(int)
			set(d, &n)
		},
	}
}

// parsePositiveSetting accepts a JSON integer that passes the CLI flag's
// rule (validatePositiveSetting). A string, a fraction, or a value too large
// to be a sensible count or duration is rejected.
func parsePositiveSetting(flag string, raw json.RawMessage) (int, error) {
	text := strings.TrimSpace(string(raw))
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: must be a positive integer", flag)
	}
	if n > math.MaxInt32 {
		return 0, fmt.Errorf("invalid %s: must be at most %d", flag, math.MaxInt32)
	}
	if err := validatePositiveSetting(flag, int(n)); err != nil {
		return 0, err
	}
	return int(n), nil
}

func parseLoggingLevel(raw json.RawMessage) (any, error) {
	var level string
	if err := json.Unmarshal(raw, &level); err != nil || level == "" ||
		!allowedLoggingLevels[level] {
		return nil, fmt.Errorf(
			"invalid logging-level: must be one of %s",
			getAllowedConfigLevelsString(", "),
		)
	}
	return level, nil
}

// settingValue renders a value for an acknowledgement.
func settingValue(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return data
}

// desiredUpdate is the outcome of reading one desired document.
type desiredUpdate struct {
	version int64
	acks    map[string]mqtt.SettingAck
	// changed holds the keys whose value in effect changed, with the value to
	// write to the config file.
	changed map[string]json.RawMessage
	device  agent.Device
}

// liveSettings is the service's view of the settings in effect: the device as
// loaded from the config file and then changed by the twin. Everything that
// reads a setting the twin can change at run time reads it here.
type liveSettings struct {
	mu      sync.RWMutex
	device  agent.Device
	loaded  bool
	version int64
}

// liveDeviceOr returns the settings in effect, or fallback before the service
// has loaded its configuration (tests drive the command path directly).
func (svc *serviceContext) liveDeviceOr(fallback agent.Device) agent.Device {
	svc.settings.mu.RLock()
	defer svc.settings.mu.RUnlock()
	if !svc.settings.loaded {
		return fallback
	}
	return svc.settings.device
}

// currentDevice is the settings in effect, for the updater.
func (svc *serviceContext) currentDevice() agent.Device {
	return svc.liveDeviceOr(agent.Device{})
}

func (svc *serviceContext) setLiveDevice(d agent.Device) {
	svc.settings.mu.Lock()
	defer svc.settings.mu.Unlock()
	svc.settings.device = d
	svc.settings.loaded = true
	svc.PostbackMaxAttempts = d.ResolvedPostbackMaxAttempts()
	svc.PostbackBaseRetryBackoff = d.ResolvedPostbackBaseRetryBackoff()
}

// postbackBudget is the in-line postback retry budget in effect.
func (svc *serviceContext) postbackBudget() (int, time.Duration) {
	svc.settings.mu.RLock()
	defer svc.settings.mu.RUnlock()
	return svc.PostbackMaxAttempts, svc.PostbackBaseRetryBackoff
}

// readDesired validates a desired document against the settings in effect.
// It returns nil when the document is older than one already applied (a GET
// answer racing a PATCH, or a redelivery). Nothing is applied here.
func (svc *serviceContext) readDesired(payload []byte) (*desiredUpdate, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(payload, &doc); err != nil || doc == nil {
		return nil, fmt.Errorf("the desired properties are not a JSON object: %w", err)
	}
	var version int64
	if raw, ok := doc["$version"]; ok {
		if err := json.Unmarshal(raw, &version); err != nil {
			return nil, fmt.Errorf("the desired properties carry an invalid $version: %w", err)
		}
	}
	keys := make([]string, 0, len(doc))
	for k := range doc {
		if !strings.HasPrefix(k, "$") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)

	svc.settings.mu.RLock()
	current, applied := svc.settings.device, svc.settings.version
	svc.settings.mu.RUnlock()
	if version != 0 && version <= applied {
		return nil, nil
	}

	update := &desiredUpdate{
		version: version,
		acks:    map[string]mqtt.SettingAck{},
		changed: map[string]json.RawMessage{},
		device:  current,
	}
	unsupported := 0
	for _, key := range keys {
		raw := doc[key]
		setting, ok := runtimeSettings[key]
		if !ok {
			if unsupported < maxUnsupportedAcks {
				update.acks[key] = mqtt.SettingAck{
					Status:         settingUnsupported,
					DesiredVersion: version,
					Detail:         "not a runtime setting; ignored",
				}
			}
			unsupported++
			continue
		}
		if string(raw) == "null" {
			update.acks[key] = mqtt.SettingAck{
				Value:          settingValue(setting.get(update.device)),
				Status:         settingCleared,
				DesiredVersion: version,
				Detail:         "desired property removed; the value in effect is kept",
			}
			continue
		}
		value, err := setting.parse(raw)
		if err != nil {
			update.acks[key] = mqtt.SettingAck{
				Value:          settingValue(setting.get(update.device)),
				Status:         settingRejected,
				DesiredVersion: version,
				Detail:         err.Error(),
			}
			continue
		}
		if setting.get(update.device) != value {
			setting.set(&update.device, value)
			update.changed[key] = settingValue(value)
		}
		update.acks[key] = mqtt.SettingAck{
			Value:          settingValue(setting.get(update.device)),
			Status:         settingApplied,
			DesiredVersion: version,
		}
	}
	return update, nil
}

// applyDesired reads a desired document and applies it: the new settings take
// effect for everything that reads them live (logging, the per-command
// timeout and output ceiling, the postback budget), the auto-updater is
// started or stopped, a worker-count change ends the connection cycle at its
// next safe point so the next cycle starts with the new pool (running
// commands finish; none is killed), and the config file is rewritten so a
// restart keeps the values. It returns the acknowledgements to report, nil
// when there is nothing to report.
func (svc *serviceContext) applyDesired(
	payload []byte,
	logger hclog.Logger,
) map[string]mqtt.SettingAck {
	update, err := svc.readDesired(payload)
	if err != nil {
		logger.Warn("Ignored the device twin's desired properties", "error", err)
		return nil
	}
	if update == nil {
		logger.Debug("Desired properties already applied; ignored")
		return nil
	}

	svc.settings.mu.Lock()
	svc.settings.device = update.device
	svc.settings.version = update.version
	svc.PostbackMaxAttempts = update.device.ResolvedPostbackMaxAttempts()
	svc.PostbackBaseRetryBackoff = update.device.ResolvedPostbackBaseRetryBackoff()
	svc.settings.mu.Unlock()

	for key, ack := range update.acks {
		if ack.Status == settingRejected {
			logger.Warn(
				"Rejected a runtime setting from the device twin; the value in effect is kept",
				"setting", key,
				"desired_version", update.version,
				"error", ack.Detail,
			)
		}
	}
	if len(update.changed) == 0 {
		return update.acks
	}

	changed := make([]string, 0, len(update.changed))
	for key := range update.changed {
		changed = append(changed, key)
	}
	sort.Strings(changed)
	if err := svc.persistSettings(update.changed); err != nil {
		logger.Warn(
			"Applied runtime settings for this process but could not rewrite the config file; "+
				"a restart returns to the file's values",
			"settings", changed,
			"path", svc.ConfigFile,
			"error", err,
		)
		for _, key := range changed {
			ack := update.acks[key]
			ack.Detail = "applied for this process; the config file could not be updated: " +
				err.Error()
			update.acks[key] = ack
		}
	}

	if slices.Contains(changed, "logging_level") && svc.rootLogger != nil {
		svc.rootLogger.SetLevel(hclog.LevelFromString(string(update.device.LoggingLevel)))
	}
	if slices.Contains(changed, "disable_auto_updates") && svc.autoUpdate != nil {
		svc.autoUpdate.set(!update.device.DisableAutoUpdates)
	}
	if slices.Contains(changed, "worker_count") {
		svc.requestReconfigure()
	}
	logger.Info(
		"Applied runtime settings from the device twin",
		"settings", changed,
		"desired_version", update.version,
	)
	return update.acks
}

// persistSettings rewrites the config file with the changed keys, preserving
// every other key as written, through utils.WriteFileAtomic so a failed write
// or a power loss leaves the previous file byte-identical.
func (svc *serviceContext) persistSettings(changed map[string]json.RawMessage) error {
	if svc.ConfigFile == "" {
		return errors.New("no config file")
	}
	data, err := os.ReadFile(svc.ConfigFile)
	if err != nil {
		return err
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return err
	}
	for key, value := range changed {
		doc[key] = value
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return utils.WriteFileAtomic(utils.NewFileSystem(), svc.ConfigFile, out, utils.SecureFileMode)
}

// requestReconfigure asks the connection cycle to end at its next safe point
// so the next one starts with the settings now in effect. Never blocks.
func (svc *serviceContext) requestReconfigure() {
	if svc.reconfigure == nil {
		return
	}
	select {
	case svc.reconfigure <- struct{}{}:
	default:
	}
}

// twinEvent is one message the twin subscription delivered.
type twinEvent struct {
	topic   string
	payload []byte
}

// twinSettingsLoop subscribes the connection to the twin's desired-properties
// PATCHes and responses, asks for the whole twin so a change made while the
// agent was offline is applied now, and applies every PATCH that follows, for
// the life of the connection cycle. A failure here is a Warn and leaves the
// command path untouched: the settings then stay what the config file says.
func (svc *serviceContext) twinSettingsLoop(
	ctx context.Context,
	client mqtt.Client,
	device agent.Device,
	logger hclog.Logger,
) {
	events := make(chan twinEvent, 64)
	var overflowMu sync.Mutex
	overflowed := false
	handler := func(topic string, payload []byte) {
		select {
		case events <- twinEvent{topic: topic, payload: payload}:
		default:
			// paho's delivery goroutine must not block. A dropped PATCH is
			// recovered by asking for the whole twin again.
			overflowMu.Lock()
			overflowed = true
			overflowMu.Unlock()
		}
	}
	if err := mqtt.SubscribeTwin(
		client,
		device.MqttSubscribeTimeout(),
		ctx.Done(),
		handler,
	); err != nil {
		if ctx.Err() == nil {
			logger.Warn(
				"Runtime settings from the device twin are unavailable on this connection",
				"error", err,
			)
		}
		return
	}
	logger.Info("Subscribed to device twin desired properties")

	pending := ""
	getTimer := time.NewTimer(twinGetTimeout)
	defer getTimer.Stop()
	requestFull := func() {
		pending = strconv.FormatInt(svc.twinRequests.Add(1)+twinRequestIDBase, 10)
		if err := mqtt.RequestTwin(client, pending, utils.MqttPublishTimeout); err != nil {
			if ctx.Err() == nil {
				logger.Warn("Failed to request the device twin", "error", err)
			}
			pending = ""
			return
		}
		getTimer.Reset(twinGetTimeout)
	}
	requestFull()

	rejecting := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-getTimer.C:
			if pending != "" {
				logger.Warn(
					"The device twin did not answer the GET; desired properties apply as they change",
					"timeout",
					twinGetTimeout,
				)
				pending = ""
			}
		case ev := <-events:
			switch status, rid, isResponse := mqtt.ParseTwinResponse(ev.topic); {
			case mqtt.IsDesiredPatch(ev.topic):
				svc.reportSettings(ctx, client, svc.applyDesired(ev.payload, logger), logger)
			case isResponse && rid != "" && rid == pending:
				pending = ""
				if status != 200 {
					logger.Warn("The device twin GET failed", "status", status)
					break
				}
				var doc mqtt.TwinDocument
				if err := json.Unmarshal(ev.payload, &doc); err != nil {
					logger.Warn("The device twin GET answer is not a twin document", "error", err)
					break
				}
				if len(doc.Desired) > 0 {
					svc.reportSettings(ctx, client, svc.applyDesired(doc.Desired, logger), logger)
				}
			case isResponse && status >= 300:
				// An answer to a reported-properties PATCH: the hub refused it
				// (throttled, too large). Logged once per refusal streak.
				if !rejecting {
					logger.Warn(
						"The device twin rejected a reported-properties update",
						"status", status,
					)
				}
				rejecting = true
			case isResponse:
				rejecting = false
			}
			overflowMu.Lock()
			refetch := overflowed
			overflowed = false
			overflowMu.Unlock()
			if refetch && pending == "" {
				logger.Warn("Device twin messages arrived faster than they were applied; " +
					"reading the whole twin again")
				requestFull()
			}
		}
	}
}

// twinRequestIDBase keeps the twin GET request ids apart from the fixed id the
// reported-properties PATCHes use.
const twinRequestIDBase = 1000

// reportSettings publishes the acknowledgements under reported.settings_ack.
func (svc *serviceContext) reportSettings(
	ctx context.Context,
	client mqtt.Client,
	acks map[string]mqtt.SettingAck,
	logger hclog.Logger,
) {
	if len(acks) == 0 || ctx.Err() != nil {
		return
	}
	publish := svc.publishHealth
	if publish == nil {
		publish = func(client mqtt.Client, props mqtt.ReportedProperties) error {
			return mqtt.UpdateReportedProperties(client, props, utils.MqttPublishTimeout)
		}
	}
	err := publish(
		client,
		mqtt.ReportedProperties{AgentVersion: version.Version, SettingsAck: acks},
	)
	if err != nil && ctx.Err() == nil {
		logger.Warn("Failed to acknowledge runtime settings in the device twin", "error", err)
	}
}

// autoUpdateControl starts and stops the auto-update runner as the
// disable_auto_updates setting changes. Stopping cancels a check or download
// in progress; an installer already launched is not recalled.
type autoUpdateControl struct {
	mu        sync.Mutex
	runner    *agent.AutoUpdateRunner
	newRunner func() *agent.AutoUpdateRunner
}

func newAutoUpdateControl(newRunner func() *agent.AutoUpdateRunner) *autoUpdateControl {
	return &autoUpdateControl{newRunner: newRunner}
}

func (c *autoUpdateControl) set(enabled bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch {
	case enabled && c.runner == nil:
		c.runner = c.newRunner()
		c.runner.Start()
	case !enabled && c.runner != nil:
		c.runner.Stop()
		c.runner = nil
	}
}
