package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/mqtt"
	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/hashicorp/go-hclog"
)

// Runtime settings from the device twin (sc-129688): an allow-list of keys,
// validated like the CLI flags, applied live, persisted to the config file,
// and acknowledged per key with the desired $version.

func newSettingsTestService(t *testing.T, config string) (*serviceContext, *capturingPublisher) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := &serviceContext{ConfigFile: path}
	device, err := svc.loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	svc.setLiveDevice(device)
	svc.reconfigure = make(chan struct{}, 1)
	pub := &capturingPublisher{}
	svc.publishHealth = pub.publish
	return svc, pub
}

const settingsTestConfig = `{
  "device_id": "dev-1",
  "rewst_org_id": "org-1",
  "shared_access_key": "secret",
  "logging_level": "info",
  "command_timeout_seconds": 120,
  "some_future_field": {"kept": true}
}`

func TestReadDesired_AllowListValidationAndStatuses(t *testing.T) {
	svc, _ := newSettingsTestService(t, settingsTestConfig)
	update, err := svc.readDesired([]byte(`{
		"$version": 4,
		"logging_level": "debug",
		"command_timeout_seconds": 5,
		"worker_count": -1,
		"max_output_bytes": "lots",
		"postback_max_attempts": 2.5,
		"postback_base_retry_backoff_seconds": 9999999999,
		"disable_auto_updates": "yes",
		"shared_access_key": "stolen",
		"no_such_setting": 1,
		"$metadata": {}
	}`))
	if err != nil || update == nil {
		t.Fatalf("readDesired: %+v %v", update, err)
	}
	want := map[string]string{
		"logging_level":                       settingApplied,
		"command_timeout_seconds":             settingApplied,
		"worker_count":                        settingRejected,
		"max_output_bytes":                    settingRejected,
		"postback_max_attempts":               settingRejected,
		"postback_base_retry_backoff_seconds": settingRejected,
		"disable_auto_updates":                settingRejected,
		"shared_access_key":                   settingUnsupported,
		"no_such_setting":                     settingUnsupported,
	}
	if len(update.acks) != len(want) {
		t.Errorf("acks %+v, want %d keys", update.acks, len(want))
	}
	for key, status := range want {
		ack := update.acks[key]
		if ack.Status != status || ack.DesiredVersion != 4 {
			t.Errorf("%s: ack %+v, want status %s at version 4", key, ack, status)
		}
	}
	if string(update.acks["worker_count"].Value) != "10" ||
		!strings.Contains(update.acks["worker_count"].Detail, "invalid worker-count") {
		t.Errorf("a rejected value must report the value in effect and why: %+v",
			update.acks["worker_count"])
	}
	if string(update.acks["command_timeout_seconds"].Value) != "5" {
		t.Errorf("applied ack value %s", update.acks["command_timeout_seconds"].Value)
	}
	if _, ok := update.acks["$metadata"]; ok {
		t.Error("a $-prefixed metadata key was acknowledged")
	}
	if len(update.changed) != 2 || string(update.changed["logging_level"]) != `"debug"` {
		t.Errorf("changed %v", update.changed)
	}
	if update.device.SharedAccessKey != "secret" {
		t.Error("a key off the allow-list changed the device")
	}
	// Nothing is applied by reading.
	if got := svc.liveDeviceOr(agent.Device{}); got.LoggingLevel != "info" {
		t.Errorf("readDesired applied the logging level: %q", got.LoggingLevel)
	}
}

func TestReadDesired_ClearedAndUnchangedAndUnsupportedCap(t *testing.T) {
	svc, _ := newSettingsTestService(t, settingsTestConfig)
	doc := map[string]any{"$version": 2, "command_timeout_seconds": 120, "logging_level": nil}
	for i := range maxUnsupportedAcks + 10 {
		doc["junk_"+strings.Repeat("x", i%3)+string(rune('a'+i%26))+string(rune('a'+i/26))] = i
	}
	payload, _ := json.Marshal(doc)
	update, err := svc.readDesired(payload)
	if err != nil {
		t.Fatal(err)
	}
	if ack := update.acks["logging_level"]; ack.Status != settingCleared ||
		string(ack.Value) != `"info"` {
		t.Errorf("cleared ack %+v", ack)
	}
	if ack := update.acks["command_timeout_seconds"]; ack.Status != settingApplied {
		t.Errorf("an unchanged value must still be acknowledged as applied: %+v", ack)
	}
	if len(update.changed) != 0 {
		t.Errorf("an unchanged value or a cleared key changed something: %v", update.changed)
	}
	unsupported := 0
	for _, ack := range update.acks {
		if ack.Status == settingUnsupported {
			unsupported++
		}
	}
	if unsupported != maxUnsupportedAcks {
		t.Errorf(
			"acknowledged %d unsupported keys, want the cap %d",
			unsupported,
			maxUnsupportedAcks,
		)
	}
}

func TestApplyDesired_AppliesLivePersistsAndAcknowledges(t *testing.T) {
	svc, _ := newSettingsTestService(t, settingsTestConfig)
	var logBuf bytes.Buffer
	svc.rootLogger = hclog.New(&hclog.LoggerOptions{Output: &logBuf, Level: hclog.Info})
	acks := svc.applyDesired([]byte(`{
		"$version": 3,
		"logging_level": "debug",
		"command_timeout_seconds": 5,
		"max_output_bytes": 4096,
		"postback_max_attempts": 7,
		"postback_base_retry_backoff_seconds": 2,
		"worker_count": 3
	}`), hclog.NewNullLogger())
	for key, ack := range acks {
		if ack.Status != settingApplied || ack.Detail != "" {
			t.Errorf("%s: %+v", key, ack)
		}
	}
	live := svc.liveDeviceOr(agent.Device{})
	if live.ResolvedCommandTimeout() != 5*time.Second || live.ResolvedMaxOutputBytes() != 4096 ||
		live.ResolvedWorkerCount() != 3 || live.LoggingLevel != utils.Debug {
		t.Errorf("live device %+v", live)
	}
	if attempts, backoff := svc.postbackBudget(); attempts != 7 || backoff != 2*time.Second {
		t.Errorf("postback budget %d %s", attempts, backoff)
	}
	if !svc.rootLogger.IsDebug() {
		t.Error("the logging level was not applied to the root logger")
	}
	select {
	case <-svc.reconfigure:
	default:
		t.Error("a worker-count change did not ask the cycle to reconfigure")
	}

	// The config file carries the new values and keeps every other key.
	raw, err := os.ReadFile(svc.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"logging_level":                       `"debug"`,
		"command_timeout_seconds":             `5`,
		"worker_count":                        `3`,
		"max_output_bytes":                    `4096`,
		"postback_max_attempts":               `7`,
		"postback_base_retry_backoff_seconds": `2`,
		"shared_access_key":                   `"secret"`,
	} {
		if string(file[key]) != want {
			t.Errorf("config file %s = %s, want %s", key, file[key], want)
		}
	}
	if !strings.Contains(string(file["some_future_field"]), `"kept"`) {
		t.Errorf("a key the agent does not know was dropped: %s", raw)
	}
	reloaded, err := (&serviceContext{ConfigFile: svc.ConfigFile}).loadConfig()
	if err != nil || reloaded.ResolvedCommandTimeout() != 5*time.Second {
		t.Errorf("a restart would not keep the timeout: %+v %v", reloaded, err)
	}

	// The same version again, and an older one, are ignored.
	if svc.applyDesired(
		[]byte(`{"$version": 3, "worker_count": 9}`),
		hclog.NewNullLogger(),
	) != nil ||
		svc.applyDesired(
			[]byte(`{"$version": 2, "worker_count": 9}`),
			hclog.NewNullLogger(),
		) != nil {
		t.Error("a desired document no newer than the one applied was applied")
	}
	if svc.liveDeviceOr(agent.Device{}).ResolvedWorkerCount() != 3 {
		t.Error("a stale document changed the worker count")
	}
}

func TestApplyDesired_RejectedValueKeepsThePreviousOne(t *testing.T) {
	svc, _ := newSettingsTestService(t, settingsTestConfig)
	before, _ := os.ReadFile(svc.ConfigFile)
	acks := svc.applyDesired([]byte(`{"$version": 5, "worker_count": -1}`), hclog.NewNullLogger())
	if acks["worker_count"].Status != settingRejected {
		t.Fatalf("acks %+v", acks)
	}
	if svc.liveDeviceOr(agent.Device{}).WorkerCount != nil {
		t.Error("a rejected worker count changed the device")
	}
	after, _ := os.ReadFile(svc.ConfigFile)
	if !bytes.Equal(before, after) {
		t.Error("a rejected value rewrote the config file")
	}
	select {
	case <-svc.reconfigure:
		t.Error("a rejected worker count reconfigured the cycle")
	default:
	}
}

func TestApplyDesired_PersistFailureStillAppliesAndSaysSo(t *testing.T) {
	svc, _ := newSettingsTestService(t, settingsTestConfig)
	svc.ConfigFile = filepath.Join(t.TempDir(), "missing", "config.json")
	acks := svc.applyDesired(
		[]byte(`{"$version": 1, "command_timeout_seconds": 9}`),
		hclog.NewNullLogger(),
	)
	ack := acks["command_timeout_seconds"]
	if ack.Status != settingApplied ||
		!strings.Contains(ack.Detail, "config file could not be updated") {
		t.Errorf("ack %+v", ack)
	}
	if svc.liveDeviceOr(agent.Device{}).ResolvedCommandTimeout() != 9*time.Second {
		t.Error("a persist failure stopped the live apply")
	}
}

// fakeUpdater counts Run calls so the auto-update control can be observed.
type fakeUpdater struct {
	agent.Updater
	mu   sync.Mutex
	runs int
}

func (f *fakeUpdater) Run(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs++
	return nil
}

func TestApplyDesired_TogglesTheAutoUpdater(t *testing.T) {
	svc, _ := newSettingsTestService(t, settingsTestConfig)
	var logBuf syncBuffer
	logger := hclog.New(&hclog.LoggerOptions{Output: &logBuf, Level: hclog.Info})
	created := 0
	svc.autoUpdate = newAutoUpdateControl(func() *agent.AutoUpdateRunner {
		created++
		return agent.NewAutoUpdateRunner(logger, &fakeUpdater{}, time.Hour, 1, time.Second)
	})
	svc.autoUpdate.set(true)
	defer svc.autoUpdate.set(false)
	svc.applyDesired([]byte(`{"$version": 1, "disable_auto_updates": true}`), hclog.NewNullLogger())
	if svc.autoUpdate.runner != nil {
		t.Fatal("disable_auto_updates=true left the updater running")
	}
	if !strings.Contains(logBuf.String(), "Auto updater stopped") {
		t.Errorf("stopping the updater was not logged: %s", logBuf.String())
	}
	svc.applyDesired(
		[]byte(`{"$version": 2, "disable_auto_updates": false}`),
		hclog.NewNullLogger(),
	)
	if svc.autoUpdate.runner == nil || created != 2 {
		t.Fatalf("disable_auto_updates=false did not start a new updater (created %d)", created)
	}
	if svc.currentDevice().DisableAutoUpdates {
		t.Error("the live device does not carry the toggle")
	}
}

// twinTestClient captures the twin subscription's handler and what the loop
// publishes, so a test can play the hub.
type twinTestClient struct {
	mockMQTTClient
	mu        sync.Mutex
	handler   pahomqtt.MessageHandler
	filters   map[string]byte
	published []string
}

func (c *twinTestClient) SubscribeMultiple(
	filters map[string]byte,
	handler pahomqtt.MessageHandler,
) pahomqtt.Token {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.filters, c.handler = filters, handler
	return &mockMQTTToken{}
}

func (c *twinTestClient) Publish(topic string, _ byte, _ bool, _ interface{}) pahomqtt.Token {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.published = append(c.published, topic)
	return &mockMQTTToken{}
}

func (c *twinTestClient) deliver(topic, payload string) {
	c.mu.Lock()
	h := c.handler
	c.mu.Unlock()
	h(c, &twinMessage{topic: topic, payload: []byte(payload)})
}

func (c *twinTestClient) lastGetRID(t *testing.T) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c.mu.Lock()
		for i := len(c.published) - 1; i >= 0; i-- {
			if rid, ok := strings.CutPrefix(c.published[i], "$iothub/twin/GET/?$rid="); ok {
				c.mu.Unlock()
				return rid
			}
		}
		c.mu.Unlock()
		time.Sleep(10 * time.Millisecond) // sleep-ok: poll interval inside a bounded wait
	}
	t.Fatal("the loop never requested the twin")
	return ""
}

type twinMessage struct {
	topic   string
	payload []byte
}

func (m *twinMessage) Duplicate() bool   { return false }
func (m *twinMessage) Qos() byte         { return 0 }
func (m *twinMessage) Retained() bool    { return false }
func (m *twinMessage) Topic() string     { return m.topic }
func (m *twinMessage) MessageID() uint16 { return 0 }
func (m *twinMessage) Payload() []byte   { return m.payload }
func (m *twinMessage) Ack()              {}

func TestTwinSettingsLoop_AppliesTheGetThenPatchesAndAcknowledges(t *testing.T) {
	svc, pub := newSettingsTestService(t, settingsTestConfig)
	client := &twinTestClient{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.twinSettingsLoop(ctx, client, agent.Device{}, hclog.NewNullLogger())
		close(done)
	}()
	rid := client.lastGetRID(t)
	if _, ok := client.filters[mqtt.TwinDesiredPatchTopic]; !ok {
		t.Errorf("subscribed to %v", client.filters)
	}
	// A reported-PATCH answer and an answer to some other request are ignored.
	client.deliver("$iothub/twin/res/204/?$rid=1&$version=2", "")
	client.deliver(
		"$iothub/twin/res/200/?$rid=999999",
		`{"desired":{"$version":50,"worker_count":4}}`,
	)
	// The GET answer carries what was set while the agent was offline.
	client.deliver(
		"$iothub/twin/res/200/?$rid="+rid,
		`{"desired":{"$version":6,"command_timeout_seconds":7},"reported":{}}`,
	)
	if !pub.waitForCount(1, 5*time.Second) {
		t.Fatal("the GET's settings were not acknowledged")
	}
	if ack := pub.last().SettingsAck["command_timeout_seconds"]; ack.Status != settingApplied ||
		ack.DesiredVersion != 6 || pub.last().AgentVersion == "" {
		t.Errorf("ack %+v", pub.last())
	}
	// A later PATCH applies; an older one does not.
	client.deliver(
		"$iothub/twin/PATCH/properties/desired/?$version=5",
		`{"$version":5,"worker_count":2}`,
	)
	client.deliver(
		"$iothub/twin/PATCH/properties/desired/?$version=7",
		`{"$version":7,"max_output_bytes":2048}`,
	)
	if !pub.waitForCount(2, 5*time.Second) {
		t.Fatal("the PATCH was not acknowledged")
	}
	if pub.count() != 2 {
		t.Errorf("published %d acknowledgements, want 2 (the stale PATCH is ignored)", pub.count())
	}
	live := svc.liveDeviceOr(agent.Device{})
	if live.ResolvedCommandTimeout() != 7*time.Second || live.ResolvedMaxOutputBytes() != 2048 ||
		live.WorkerCount != nil {
		t.Errorf("live device %+v", live)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not stop with the cycle")
	}
}

func TestTwinSettingsLoop_SubscribeFailureLeavesSettingsAlone(t *testing.T) {
	svc, pub := newSettingsTestService(t, settingsTestConfig)
	client := &failingTwinClient{}
	var logBuf syncBuffer
	logger := hclog.New(&hclog.LoggerOptions{Output: &logBuf, Level: hclog.Warn})
	svc.twinSettingsLoop(context.Background(), client, agent.Device{}, logger)
	if !strings.Contains(logBuf.String(), "Runtime settings from the device twin are unavailable") {
		t.Errorf("the failure was not logged: %s", logBuf.String())
	}
	if pub.count() != 0 {
		t.Error("a failed subscription published")
	}
}

type failingTwinClient struct{ mockMQTTClient }

func (c *failingTwinClient) SubscribeMultiple(
	map[string]byte,
	pahomqtt.MessageHandler,
) pahomqtt.Token {
	return &mockMQTTToken{err: errSubscribeRefused}
}

var errSubscribeRefused = &json.UnsupportedValueError{Str: "refused"}

func TestValidatePositiveSetting_MatchesTheCLIRule(t *testing.T) {
	if validatePositiveSetting("worker-count", 1) != nil {
		t.Error("1 was rejected")
	}
	for _, v := range []int{0, -1} {
		err := validatePositiveSetting("worker-count", v)
		if err == nil || err.Error() != "invalid worker-count: must be a positive integer" {
			t.Errorf("%d: %v", v, err)
		}
	}
}

// connectCountingClient counts connection attempts.
type connectCountingClient struct {
	mockMQTTClient
	connects *atomic.Int32
}

func (c *connectCountingClient) Connect() pahomqtt.Token {
	c.connects.Add(1)
	return &mockMQTTToken{}
}

// A worker count from the twin ends the connection cycle the way a SAS
// renewal does, so the next cycle starts its pool at the new size.
func TestExecute_ReconfigureEndsTheCycleAndReconnects(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.json")
	device := agent.Device{
		DeviceId:             "test-device",
		SharedAccessKey:      "dGVzdC1zaGFyZWQta2V5LXRoYXQtaXMtbG9uZy1lbm91Z2gtZm9yLWJhc2U2NC1kZWNvZGluZw==",
		AzureIotHubHost:      "test.azure-devices.net",
		LoggingLevel:         "error",
		DisableAutoUpdates:   true,
		DisableAgentPostback: true,
	}
	configBytes, _ := json.Marshal(device)
	if err := os.WriteFile(configPath, configBytes, utils.DefaultFileMod); err != nil {
		t.Fatal(err)
	}
	var connects atomic.Int32
	origNewClient := mqtt.NewClient
	mqtt.NewClient = func(_ *pahomqtt.ClientOptions) pahomqtt.Client {
		return &connectCountingClient{connects: &connects}
	}
	defer func() { mqtt.NewClient = origNewClient }()

	svc := &serviceContext{
		ConfigFile: configPath,
		LogFile:    filepath.Join(tmpDir, "test.log"),
		OrgId:      "test-org",
		Executor:   &mockExecutor{},
	}
	stop := make(chan struct{})
	running := make(chan struct{}, 1)
	done := make(chan service.ServiceExitCode, 1)
	go func() { done <- svc.Execute(stop, running) }()
	<-running
	waitFor := func(n int32) bool {
		deadline := time.Now().Add(10 * time.Second)
		for connects.Load() < n && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond) // sleep-ok: poll interval inside a bounded wait
		}
		return connects.Load() >= n
	}
	if !waitFor(1) {
		t.Fatal("the service never connected")
	}
	svc.requestReconfigure()
	if !waitFor(2) {
		t.Fatal("a reconfigure did not end the cycle and reconnect")
	}
	close(stop)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Execute did not exit")
	}
}
