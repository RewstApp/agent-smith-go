package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/syslog"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/hashicorp/go-hclog"
)

// sc-132991: the reporter's intervals, log rotation, syslog and proxy_url are
// twin settings, each validated like its CLI flag, applied while the service
// runs, persisted and acknowledged.

const runtimeProxyPass = "s3cret-proxy"

// configFileValues reads the config file back as raw JSON per key.
func configFileValues(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]json.RawMessage
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	return file
}

// Every new key: a valid value is applied and persisted, an invalid one is
// rejected naming the flag with the value in effect kept, and a removed key is
// cleared with the value in effect kept.
func TestApplyDesired_NewKeysAppliedRejectedClearedPersisted(t *testing.T) {
	clearRuntimeProxyEnv(t)
	cases := []struct {
		key, valid, appliedValue string
		invalid                  []string
		flag                     string
		defaultValue             string
	}{
		{
			key: "health_report_interval_seconds", valid: "5", appliedValue: "5",
			invalid: []string{"0", "-1", `"60"`, "1.5"}, flag: "health-report-interval-seconds",
			defaultValue: "60",
		},
		{
			key: "command_heartbeat_seconds", valid: "7", appliedValue: "7",
			invalid: []string{"0", "true", "99999999999"}, flag: "command-heartbeat-seconds",
			defaultValue: "30",
		},
		{
			key: "log_max_bytes", valid: "8192", appliedValue: "8192",
			invalid: []string{"0", `"big"`}, flag: "log-max-bytes",
			defaultValue: "10485760",
		},
		{
			key: "log_max_files", valid: "2", appliedValue: "2",
			invalid: []string{"-3", "{}"}, flag: "log-max-files",
			defaultValue: "5",
		},
		{
			key: "syslog", valid: "true", appliedValue: "true",
			invalid: []string{`"yes"`, "1"}, flag: "syslog",
			defaultValue: "false",
		},
		{
			key: "proxy_url", valid: `"http://proxy.corp:3128"`,
			appliedValue: `"http://proxy.corp:3128"`,
			invalid:      []string{`"ftp://proxy.corp:21"`, `"http://"`, "3128"}, flag: "proxy-url",
			defaultValue: `""`,
		},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			svc, _ := newSettingsTestService(t, settingsTestConfig)
			version := int64(1)
			for _, bad := range c.invalid {
				before, _ := os.ReadFile(svc.ConfigFile)
				acks := svc.applyDesired(
					[]byte(`{"$version": `+itoa(version)+`, "`+c.key+`": `+bad+`}`),
					false,
					hclog.NewNullLogger(),
				)
				version++
				ack := acks[c.key]
				if ack.Status != settingRejected ||
					!strings.Contains(ack.Detail, "invalid "+c.flag) ||
					string(ack.Value) != c.defaultValue {
					t.Errorf("%s=%s: ack %+v (value %s), want rejected naming %s with %s kept",
						c.key, bad, ack, ack.Value, c.flag, c.defaultValue)
				}
				after, _ := os.ReadFile(svc.ConfigFile)
				if !bytes.Equal(before, after) {
					t.Errorf("%s=%s: a rejected value rewrote the config file", c.key, bad)
				}
			}

			acks := svc.applyDesired(
				[]byte(`{"$version": `+itoa(version)+`, "`+c.key+`": `+c.valid+`}`),
				false,
				hclog.NewNullLogger(),
			)
			version++
			if ack := acks[c.key]; ack.Status != settingApplied || ack.Detail != "" ||
				string(ack.Value) != c.appliedValue {
				t.Errorf("valid %s: ack %+v (value %s)", c.key, ack, ack.Value)
			}
			if got := string(configFileValues(t, svc.ConfigFile)[c.key]); got != c.appliedValue {
				t.Errorf("config file %s = %s, want %s", c.key, got, c.appliedValue)
			}
			reloaded, err := (&serviceContext{ConfigFile: svc.ConfigFile}).loadConfig()
			if err != nil {
				t.Fatal(err)
			}
			if got := string(runtimeSettings[c.key].ackValue(reloaded)); got != c.appliedValue {
				t.Errorf("a restart reads %s = %s, want %s", c.key, got, c.appliedValue)
			}

			acks = svc.applyDesired(
				[]byte(`{"$version": `+itoa(version)+`, "`+c.key+`": null}`),
				false,
				hclog.NewNullLogger(),
			)
			if ack := acks[c.key]; ack.Status != settingCleared ||
				string(ack.Value) != c.appliedValue {
				t.Errorf("removed %s: ack %+v (value %s), want cleared keeping %s",
					c.key, ack, ack.Value, c.appliedValue)
			}
		})
	}
}

func itoa(n int64) string {
	data, _ := json.Marshal(n)
	return string(data)
}

// A shorter health interval reaches a running reporter at once: with the
// reporter on an hour-long interval, the health goes out within a few seconds
// of the twin setting it to one. Before sc-132991 a new interval needed a
// reinstall, and it would not have reached this loop before the next cycle.
func TestApplyDesired_HealthIntervalRetimesTheRunningReporter(t *testing.T) {
	svc, pub := newHealthTestService(t)
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(settingsTestConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	svc.ConfigFile = path
	svc.setLiveDevice(agent.Device{})
	svc.healthRetime = make(chan struct{}, 1)
	stop := startHealthLoop(t, svc, 3600, 3600)
	defer stop()
	if pub.count() != 0 {
		t.Fatalf("published %d times before any tick", pub.count())
	}
	svc.spool.countDrop(dropReasonExpired) // a change for the next tick to publish
	acks := svc.applyDesired(
		[]byte(`{"$version": 1, "health_report_interval_seconds": 1}`),
		false,
		hclog.NewNullLogger(),
	)
	if acks["health_report_interval_seconds"].Status != settingApplied {
		t.Fatalf("acks %+v", acks)
	}
	if !pub.waitForCount(1, 5*time.Second) {
		t.Fatal("the reporter kept the hour-long interval after the twin shortened it")
	}
}

// The heartbeat interval is re-armed the same way, and an unchanged value
// sends nothing to the reporter.
func TestApplyDesired_RetimeOnlyWhenAnIntervalChanged(t *testing.T) {
	svc, _ := newSettingsTestService(t, settingsTestConfig)
	svc.healthRetime = make(chan struct{}, 1)
	svc.applyDesired(
		[]byte(`{"$version": 1, "command_heartbeat_seconds": 30, "worker_count": 10}`),
		false,
		hclog.NewNullLogger(),
	)
	select {
	case <-svc.healthRetime:
		t.Error("an unchanged heartbeat interval retimed the reporter")
	default:
	}
	svc.applyDesired(
		[]byte(`{"$version": 2, "command_heartbeat_seconds": 2}`),
		false,
		hclog.NewNullLogger(),
	)
	select {
	case <-svc.healthRetime:
	default:
		t.Error("a new heartbeat interval did not retime the reporter")
	}
	if got := svc.currentDevice().ResolvedCommandHeartbeatInterval(); got != 2*time.Second {
		t.Errorf("live heartbeat interval %s", got)
	}
}

// log_max_bytes and log_max_files reach the log writer: the next rotation
// uses the new ceiling, and a lowered count discards the copies past it.
func TestApplyDesired_LogLimitsApplyToTheLogWriter(t *testing.T) {
	svc, _ := newSettingsTestService(t, settingsTestConfig)
	logPath := filepath.Join(t.TempDir(), "agent.log")
	writer, err := utils.NewRotatingFile(logPath, 1<<20, 4, utils.DefaultFileMod, "agent_smith")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	svc.logWriter = writer
	for i := 1; i <= 4; i++ {
		if err := os.WriteFile(logPath+"."+itoa(int64(i)), []byte("old\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	svc.applyDesired(
		[]byte(`{"$version": 1, "log_max_bytes": 64, "log_max_files": 2}`),
		false,
		hclog.NewNullLogger(),
	)
	if maxBytes, maxFiles := writer.Limits(); maxBytes != 64 || maxFiles != 2 {
		t.Fatalf("writer limits %d %d, want 64 2", maxBytes, maxFiles)
	}
	line := strings.Repeat("x", 40) + "\n"
	for range 3 {
		if _, err := writer.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	for i, want := range map[int]bool{1: true, 2: true, 3: false, 4: false} {
		_, err := os.Stat(logPath + "." + itoa(int64(i)))
		if exists := err == nil; exists != want {
			t.Errorf("copy %d exists=%v, want %v", i, exists, want)
		}
	}
	if fi, err := os.Stat(logPath); err != nil || fi.Size() > 64 {
		t.Errorf("active log not rotated at the new ceiling: %v %v", fi, err)
	}

	// Only one of the two named still sets both, from the settings in effect.
	svc.applyDesired(
		[]byte(`{"$version": 2, "log_max_files": 3}`),
		false,
		hclog.NewNullLogger(),
	)
	if maxBytes, maxFiles := writer.Limits(); maxBytes != 64 || maxFiles != 3 {
		t.Errorf("writer limits %d %d, want 64 3", maxBytes, maxFiles)
	}
}

// fakeSyslog records what reached the system log and whether it was closed.
type fakeSyslog struct {
	mu     sync.Mutex
	lines  []string
	out    io.Writer
	closed bool
}

func (f *fakeSyslog) Write(p []byte) (int, error) {
	f.mu.Lock()
	f.lines = append(f.lines, string(p))
	f.mu.Unlock()
	return f.out.Write(p)
}

func (f *fakeSyslog) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

func (f *fakeSyslog) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.lines...)
}

// syslog switches the logger's sink at once: lines reach the system log only
// while it is on, the log file gets every line either way, and turning it off
// closes the forwarder.
func TestApplyDesired_SyslogSwitchesTheLogSink(t *testing.T) {
	svc, _ := newSettingsTestService(t, settingsTestConfig)
	var file syncBuffer
	var opened []*fakeSyslog
	svc.logSink = newLogSink(&file, func(out io.Writer) (syslog.Syslog, error) {
		s := &fakeSyslog{out: out}
		opened = append(opened, s)
		return s, nil
	})
	logger := hclog.New(&hclog.LoggerOptions{Output: svc.logSink, Level: hclog.Info})

	logger.Info("before")
	acks := svc.applyDesired([]byte(`{"$version": 1, "syslog": true}`), false, logger)
	if acks["syslog"].Status != settingApplied || len(opened) != 1 || !svc.logSink.Syslog() {
		t.Fatalf("acks %+v opened %d", acks, len(opened))
	}
	logger.Info("while on")
	svc.applyDesired([]byte(`{"$version": 2, "syslog": false}`), false, logger)
	logger.Info("after")

	forwarded := strings.Join(opened[0].got(), "")
	if strings.Contains(forwarded, "before") || strings.Contains(forwarded, "after") ||
		!strings.Contains(forwarded, "while on") ||
		!strings.Contains(forwarded, "Applied runtime settings") {
		t.Errorf("forwarded while on only? %q", forwarded)
	}
	for _, line := range []string{"before", "while on", "after"} {
		if strings.Count(file.String(), line) != 1 {
			t.Errorf("the log file has %q %d times: %s", line, strings.Count(file.String(), line),
				file.String())
		}
	}
	if !opened[0].closed || svc.logSink.Syslog() {
		t.Error("turning syslog off did not close the forwarder")
	}
}

// A forwarder that cannot be opened leaves the file as the only sink and says
// so in the acknowledgement; the setting is still persisted for the restart.
func TestApplyDesired_SyslogOpenFailureIsReported(t *testing.T) {
	svc, _ := newSettingsTestService(t, settingsTestConfig)
	var file syncBuffer
	svc.logSink = newLogSink(&file, func(io.Writer) (syslog.Syslog, error) {
		return nil, errors.New("event log unavailable")
	})
	acks := svc.applyDesired(
		[]byte(`{"$version": 1, "syslog": true}`),
		false,
		hclog.New(&hclog.LoggerOptions{Output: &file}),
	)
	ack := acks["syslog"]
	if ack.Status != settingApplied || !strings.Contains(ack.Detail, "event log unavailable") {
		t.Errorf("ack %+v", ack)
	}
	if svc.logSink.Syslog() {
		t.Error("a forwarder that failed to open is in use")
	}
	if !strings.Contains(file.String(), "Failed to switch forwarding to the system log") {
		t.Errorf("the failure was not logged: %s", file.String())
	}
}

// The sink is safe to switch while other goroutines log (go test -race).
func TestLogSink_SwitchWhileWriting(t *testing.T) {
	var file syncBuffer
	sink := newLogSink(&file, func(out io.Writer) (syslog.Syslog, error) {
		return &fakeSyslog{out: out}, nil
	})
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_, _ = sink.Write([]byte("line\n"))
				}
			}
		}()
	}
	for i := range 200 {
		if err := sink.SetSyslog(i%2 == 0); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	if err := sink.SetSyslog(true); err != nil || sink.SetSyslog(true) != nil || !sink.Syslog() {
		t.Error("switching to the state in effect is not a no-op")
	}
}

func clearRuntimeProxyEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy",
		"REQUEST_METHOD",
	} {
		t.Setenv(name, "")
	}
}

func proxyFor(t *testing.T, fn func(*http.Request) (*url.URL, error), target string) string {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}
	return utils.DescribeProxy(utils.ProxyFor(fn, u))
}

// proxy_url reaches every client: the cycle is ended so the next connection
// dials through it, the postback client and updater (liveProxy) use it from
// the next request, its credentials are masked in the acknowledgement and
// every log line and kept in the config file, and clearing it returns to the
// environment's proxy.
func TestApplyDesired_ProxyURLAppliesLiveAndMasksCredentials(t *testing.T) {
	clearRuntimeProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "http://env-proxy:8080")
	svc, _ := newSettingsTestService(t, settingsTestConfig)
	initial, _ := utils.ResolveProxy("")
	svc.proxyFn.Store(&initial)
	svc.HTTPClient = &http.Client{Transport: utils.ProxyTransport(svc.liveProxy)}
	var logs syncBuffer
	logger := hclog.New(&hclog.LoggerOptions{Output: &logs, Level: hclog.Debug})
	const target = "https://engine.example.com/webhooks/custom/action/p1"
	if got := proxyFor(t, svc.liveProxy, target); got != "http://env-proxy:8080" {
		t.Fatalf("before: %s", got)
	}

	raw := "http://agent:" + runtimeProxyPass + "@proxy.corp:3128"
	acks := svc.applyDesired(
		[]byte(`{"$version": 1, "proxy_url": " `+raw+` "}`),
		false,
		logger,
	)
	ack := acks["proxy_url"]
	if ack.Status != settingApplied || string(ack.Value) != `"http://agent:xxxxx@proxy.corp:3128"` {
		t.Errorf("ack %+v value %s", ack, ack.Value)
	}
	if got := proxyFor(t, svc.liveProxy, target); got != "http://agent:xxxxx@proxy.corp:3128" {
		t.Errorf("the postback client and the updater do not see the new proxy: %s", got)
	}
	if got := proxyFor(t, svc.currentProxy(), target); got != "http://agent:xxxxx@proxy.corp:3128" {
		t.Errorf("the next connection would not use the new proxy: %s", got)
	}
	select {
	case <-svc.reconfigure:
	default:
		t.Error("a proxy_url change did not end the connection cycle")
	}
	if got := string(configFileValues(t, svc.ConfigFile)["proxy_url"]); got != `"`+raw+`"` {
		t.Errorf("config file proxy_url %s, want the URL as given, trimmed", got)
	}
	if strings.Contains(logs.String(), runtimeProxyPass) {
		t.Errorf("the proxy password reached the log: %s", logs.String())
	}
	if !strings.Contains(logs.String(), "proxy.corp:3128") {
		t.Errorf("the new proxy was not logged: %s", logs.String())
	}

	// A rejection names the problem without the credentials and keeps the
	// proxy in effect.
	acks = svc.applyDesired(
		[]byte(`{"$version": 2, "proxy_url": "socks5://agent:`+runtimeProxyPass+`@proxy:1080"}`),
		false,
		logger,
	)
	ack = acks["proxy_url"]
	if ack.Status != settingRejected || strings.Contains(ack.Detail, runtimeProxyPass) ||
		strings.Contains(string(ack.Value), runtimeProxyPass) {
		t.Errorf("rejected ack %+v value %s", ack, ack.Value)
	}
	if got := proxyFor(t, svc.liveProxy, target); got != "http://agent:xxxxx@proxy.corp:3128" {
		t.Errorf("a rejected proxy_url replaced the one in effect: %s", got)
	}
	select {
	case <-svc.reconfigure:
		t.Error("a rejected proxy_url ended the connection cycle")
	default:
	}

	// Cleared to "" returns to the environment.
	acks = svc.applyDesired([]byte(`{"$version": 3, "proxy_url": ""}`), false, logger)
	if acks["proxy_url"].Status != settingApplied {
		t.Errorf("ack %+v", acks["proxy_url"])
	}
	if got := proxyFor(t, svc.liveProxy, target); got != "http://env-proxy:8080" {
		t.Errorf("clearing proxy_url did not return to the environment: %s", got)
	}
	if strings.Contains(logs.String(), runtimeProxyPass) {
		t.Errorf("the proxy password reached the log: %s", logs.String())
	}
}

// Before Execute resolves a proxy, liveProxy reads the environment, as the
// clients it stands in for did.
func TestLiveProxy_BeforeExecuteReadsTheEnvironment(t *testing.T) {
	clearRuntimeProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "http://env-proxy:8080")
	svc := &serviceContext{}
	if svc.currentProxy() != nil {
		t.Error("a proxy before Execute")
	}
	got := proxyFor(t, svc.liveProxy, "https://engine.example.com/")
	if got != "http://env-proxy:8080" {
		t.Errorf("liveProxy before Execute: %s", got)
	}
}
