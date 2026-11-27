package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/mqtt"
)

// --diagnostic --json (sc-132994): one JSON document of every non-interactive
// check, an exit code a script can branch on, and none of the secrets.

const reportOrg = "5a0c3f0e-8a52-4d5e-9d3c-2f1b7c6d9e10"

// newReportRoot lays out one installed agent behind a proxy, with a running
// command whose last output line the report must withhold, and points the
// scripts directory into it.
func newReportRoot(t *testing.T, config map[string]any) (root, dataDir string) {
	t.Helper()
	clearProxyEnv(t)
	root = t.TempDir()
	dataDir = filepath.Join(root, reportOrg)
	if config == nil {
		config = map[string]any{
			"device_id":          "dev-1",
			"rewst_org_id":       reportOrg,
			"shared_access_key":  bundleSharedKey,
			"github_token":       bundleGithubToken,
			"proxy_url":          "http://agent:" + bundleProxyPass + "@proxy.example:3128",
			"azure_iot_hub_host": "hub.example",
			"rewst_engine_host":  "engine.example",
		}
	}
	writeJSON(t, filepath.Join(dataDir, "config.json"), config)
	writeJSON(t, filepath.Join(dataDir, healthFileName), mqtt.ReportedProperties{
		AgentVersion: "v1.11.0-test",
		Health:       &mqtt.Health{ReportedAt: "2026-11-27T11:59:00Z", UptimeSeconds: 42},
		Commands: &mqtt.CommandsProgress{InFlight: []mqtt.CommandProgress{{
			PostID:         "running-1",
			StartedAt:      "2026-11-27T11:58:00Z",
			ElapsedSeconds: 60,
			LastOutputLine: "token: " + bundleOutputMark,
		}}},
	})
	agent.SetScriptsDirectoryOverrideForTesting(func(string) string {
		return filepath.Join(dataDir, "scripts")
	})
	t.Cleanup(func() { agent.SetScriptsDirectoryOverrideForTesting(nil) })
	return root, dataDir
}

func reportParams(running bool) *diagnosticContext {
	return &diagnosticContext{
		OrgId:          reportOrg,
		Diagnostic:     true,
		JSON:           true,
		ServiceManager: &mockServiceManager{openService: &mockService{isActive: running}},
		Sys:            &mockSystemInfoProvider{hostname: "host", hostPlatform: "linux"},
		Domain:         &mockDomainInfoProvider{},
	}
}

// proxyOnlyDialer is a network where only the proxy gets out: both direct
// hub ports and the direct engine handshake fail, everything through the
// proxy works, and the engine answers the HEAD with 405 as the stub does.
func proxyOnlyDialer() *mockTLSDialer {
	through := true
	return &mockTLSDialer{
		direct:     map[string]bool{"8883": false, "443": false},
		viaProxy:   &through,
		headStatus: 405,
	}
}

func runReport(
	t *testing.T,
	root string,
	params *diagnosticContext,
	dialer tlsDialer,
) (int, []byte, error) {
	t.Helper()
	var out bytes.Buffer
	code, err := runDiagnosticJSON(context.Background(), params, diagnosticEnv{
		root:   root,
		dialer: dialer,
		now:    func() time.Time { return time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC) },
		stdout: &out,
	})
	return code, out.Bytes(), err
}

// checksByName decodes the document's checks, failing on any without the
// common keys and their types.
func checksByName(t *testing.T, doc map[string]any) (map[string]map[string]any, []string) {
	t.Helper()
	raw, ok := doc["checks"].([]any)
	if !ok {
		t.Fatalf("checks is not an array: %v", doc["checks"])
	}
	byName := map[string]map[string]any{}
	var names []string
	for _, item := range raw {
		c, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("a check is not an object: %v", item)
		}
		for key, kind := range map[string]string{
			"name": "string", "section": "string", "detail": "string",
			"ok": "bool", "advisory": "bool",
		} {
			v, present := c[key]
			if !present {
				t.Fatalf("check %v lacks %q", c, key)
			}
			if got := reflect.TypeOf(v).Kind().String(); got != kind {
				t.Fatalf("check %v: %s is a %s, not a %s", c, key, got, kind)
			}
		}
		name := c["name"].(string)
		byName[name] = c
		names = append(names, name)
	}
	return byName, names
}

// A healthy agent behind a proxy-only egress passes: its direct paths fail,
// but they are advisory - the transport and the engine's answer through the
// proxy are the verdicts - so the exit is 0, as an RMM script needs it to be.
func TestRunDiagnosticJSON_AHealthyAgentBehindAProxyExitsZero(t *testing.T) {
	root, _ := newReportRoot(t, nil)
	console, _ := captureDiagnostic(t)

	code, out, err := runReport(t, root, reportParams(true), proxyOnlyDialer())
	if err != nil || code != diagnosticExitPassed {
		t.Fatalf("code %d err %v\n%s", code, err, out)
	}
	if console.Len() != 0 {
		t.Errorf("the console rendering went out beside the document:\n%s", console)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("not one JSON document: %v\n%s", err, out)
	}
	for key, want := range map[string]any{
		"org_id":        reportOrg,
		"agent_version": "v1.11.0-test",
		"generated_at":  "2026-11-27T12:00:00Z",
		"ok":            true,
	} {
		if doc[key] != want {
			t.Errorf("%s = %v, want %v", key, doc[key], want)
		}
	}
	if failed, ok := doc["failed"].([]any); !ok || len(failed) != 0 {
		t.Errorf("failed = %v, want an empty array", doc["failed"])
	}
	for _, key := range []string{"diagnostic_version", "platform"} {
		if s, _ := doc[key].(string); s == "" {
			t.Errorf("%s is empty", key)
		}
	}

	checks, names := checksByName(t, doc)
	want := []string{
		checkConfigLoad, checkServiceStatus, checkCommandExecution,
		checkHubDirect8883, checkHubDirect443, checkHubProxy443, checkHubTransport,
		checkEngineDirect, checkEngineProxy, checkEngineReachable, checkEngineSpool,
		checkScriptsDirSecure, checkScriptsDirWrite, checkScriptsDirReadBack,
		checkDataDirExists, checkFleetHealth, checkHostInfo,
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("checks\n got %v\nwant %v", names, want)
	}
	for _, name := range []string{
		checkHubDirect8883,
		checkHubDirect443,
		checkEngineDirect,
	} {
		if c := checks[name]; c["ok"] != false || c["advisory"] != true {
			t.Errorf("%s: %v, want a failed advisory result", name, c)
		}
	}
	proxied := checks[checkHubProxy443]
	if proxied["ok"] != true || proxied["advisory"] != true ||
		proxied["proxy"] != "http://agent:xxxxx@proxy.example:3128" ||
		proxied["proxy_source"] != "proxy_url" ||
		proxied["section"] != "MQTT/WebSocket Connectivity" {
		t.Errorf("hub.proxy_443: %v", proxied)
	}
	if c := checks[checkHubTransport]; c["ok"] != true || c["advisory"] != false ||
		!strings.Contains(c["transport"].(string), "through http://agent:xxxxx@proxy.example") {
		t.Errorf("hub.transport: %v", c)
	}
	if c := checks[checkEngineReachable]; c["ok"] != true || c["status"] != float64(405) {
		t.Errorf("engine.reachable: %v", c)
	}
	if c := checks[checkServiceStatus]; c["state"] != "running" {
		t.Errorf("service.status: %v", c)
	}
	if c := checks[checkEngineSpool]; c["waiting"] != float64(0) {
		t.Errorf("engine.spool: %v", c)
	}

	health, _ := doc["health"].(map[string]any)
	commands, _ := health["commands"].(map[string]any)
	inFlight, _ := commands["in_flight"].([]any)
	if len(inFlight) != 1 ||
		inFlight[0].(map[string]any)["last_output_line"] != reportWithheld {
		t.Errorf("health.commands: %v", health["commands"])
	}
	host, _ := doc["host"].(map[string]any)
	if host["hostname"] != "host" || host["org_id"] != reportOrg {
		t.Errorf("host: %v", doc["host"])
	}
	for _, secret := range []string{
		bundleSharedKey, bundleGithubToken, bundleProxyPass, bundleOutputMark,
	} {
		if bytes.Contains(out, []byte(secret)) {
			t.Errorf("the document carries %q", secret)
		}
	}
}

// Every failing verdict is named in failed, in the order the checks ran, and
// the exit is 2; advisory results never are.
func TestRunDiagnosticJSON_FailingChecksExitTwoAndAreNamed(t *testing.T) {
	root, dataDir := newReportRoot(t, nil)
	writeJSON(t, filepath.Join(dataDir, "postback_spool", "e1.json"), spoolEntry{
		PostId:    "spooled-1",
		Result:    []byte(`{"output":"` + bundleResultMark + `"}`),
		CreatedAt: time.Date(2026, 11, 27, 11, 0, 0, 0, time.UTC),
	})
	dialer := &mockTLSDialer{headErr: errors.New("connection refused")}

	code, out, err := runReport(t, root, reportParams(false), dialer)
	if err != nil || code != diagnosticExitFailed {
		t.Fatalf("code %d err %v\n%s", code, err, out)
	}
	var doc struct {
		OK     bool     `json:"ok"`
		Failed []string `json:"failed"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	want := []string{checkServiceStatus, checkHubTransport, checkEngineReachable, checkEngineSpool}
	if doc.OK || !reflect.DeepEqual(doc.Failed, want) {
		t.Errorf("ok %v failed %v, want false %v", doc.OK, doc.Failed, want)
	}
	var full map[string]any
	_ = json.Unmarshal(out, &full)
	checks, _ := checksByName(t, full)
	if c := checks[checkEngineSpool]; c["waiting"] != float64(1) ||
		c["oldest_created_at"] != "2026-11-27T11:00:00Z" {
		t.Errorf("engine.spool: %v", c)
	}
	if c := checks[checkServiceStatus]; c["state"] != "stopped" {
		t.Errorf("service.status: %v", c)
	}
	if bytes.Contains(out, []byte(bundleResultMark)) {
		t.Error("the document carries a spooled result")
	}
}

// A config.json that is JSON but not the agent's configuration is a finding,
// reported first, not a refusal: the document is still written, and the
// secrets the file does hold are still scrubbed from it.
func TestRunDiagnosticJSON_AConfigThatDoesNotLoadIsReportedFirst(t *testing.T) {
	root, _ := newReportRoot(t, map[string]any{
		"device_id":         "dev-1",
		"shared_access_key": bundleSharedKey,
		"worker_count":      "lots",
	})

	code, out, err := runReport(t, root, reportParams(true), proxyOnlyDialer())
	if err != nil || code != diagnosticExitFailed {
		t.Fatalf("code %d err %v\n%s", code, err, out)
	}
	var doc map[string]any
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatal(err)
	}
	checks, names := checksByName(t, doc)
	if names[0] != checkConfigLoad || checks[checkConfigLoad]["ok"] != false ||
		!strings.Contains(checks[checkConfigLoad]["detail"].(string), "worker_count") {
		t.Errorf("the first check is not the failed configuration: %v", checks[names[0]])
	}
	for _, name := range []string{checkHubConfigured, checkEngineConfigured} {
		if c, ok := checks[name]; !ok || c["ok"] != false {
			t.Errorf("%s: %v", name, c)
		}
	}
	if bytes.Contains(out, []byte(bundleSharedKey)) {
		t.Error("the document carries the shared access key")
	}
}

// No document is written when there is no agent to report on: exit 1 with
// the reason, which says what to do.
func TestRunDiagnosticJSON_NoTargetIsAnError(t *testing.T) {
	params := reportParams(true)
	params.OrgId = ""
	code, out, err := runReport(t, t.TempDir(), params, proxyOnlyDialer())
	if code != diagnosticExitError || err == nil ||
		!strings.Contains(err.Error(), "nothing to diagnose") || len(out) != 0 {
		t.Errorf("empty root: code %d err %v out %q", code, err, out)
	}

	root := t.TempDir()
	for _, org := range []string{reportOrg, bundleOrg} {
		writeJSON(t, filepath.Join(root, org, "config.json"), map[string]any{"device_id": org})
	}
	code, out, err = runReport(t, root, params, proxyOnlyDialer())
	if code != diagnosticExitError || err == nil ||
		!strings.Contains(err.Error(), "pass --org-id") || len(out) != 0 {
		t.Errorf("two agents: code %d err %v out %q", code, err, out)
	}

	params.OrgId = reportOrg
	code, _, err = runReport(t, t.TempDir(), params, proxyOnlyDialer())
	if code != diagnosticExitError || err == nil ||
		!strings.Contains(err.Error(), "no agent for org "+reportOrg) {
		t.Errorf("unknown org: code %d err %v", code, err)
	}
}

// The console lines, diagnostic.txt and the document are renderings of one
// result: every recorded result is a [PASS]/[FAIL] line the console printed
// under its section, and every such line was recorded.
func TestRecordChecks_RecordsWhatTheConsolePrints(t *testing.T) {
	root, _ := newReportRoot(t, nil)
	console, _ := captureDiagnostic(t)
	target := agentInfoIn(root, reportOrg)
	device := agent.Device{
		AzureIotHubHost: "hub.example",
		RewstEngineHost: "engine.example",
		ProxyURL:        "http://agent:" + bundleProxyPass + "@proxy.example:3128",
	}
	target.Device = &device

	checks := recordChecks(func() {
		runReportSections(
			context.Background(), reportParams(true), target, proxyOnlyDialer(),
			time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC), nil,
		)
	})
	printed := strings.Count(console.String(), "[PASS] ") +
		strings.Count(console.String(), "[FAIL] ")
	if printed != len(checks) {
		t.Errorf("printed %d result lines, recorded %d", printed, len(checks))
	}
	for _, c := range checks {
		mark := "[FAIL] "
		if c.OK {
			mark = "[PASS] "
		}
		section := strings.Index(console.String(), "── "+c.Section+" ──")
		line := strings.Index(console.String(), mark+c.Detail+"\n")
		if c.Name == "" || section < 0 || line < section {
			t.Errorf("%+v is not a line printed under its section", c)
		}
	}
	// Nothing is recorded outside recordChecks.
	runCommandTest()
	if diagChecks != nil {
		t.Error("the recorder outlived recordChecks")
	}
}

func TestCheckResult_MarshalJSON_PutsTheCommonKeysFirst(t *testing.T) {
	out, err := checkResult{
		Name:    "engine.reachable",
		Section: "Engine Connectivity",
		OK:      true,
		Detail:  "HEAD https://e/x?a=1&b=2 <ok>",
		Fields:  map[string]any{"z": 1, "a": "first", "ok": "cannot replace a common key"},
	}.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"engine.reachable","section":"Engine Connectivity","ok":true,` +
		`"advisory":false,"detail":"HEAD https://e/x?a=1&b=2 <ok>","a":"first","z":1}`
	if string(out) != want {
		t.Errorf("\n got %s\nwant %s", out, want)
	}
}

func TestNewDiagnosticContext_JSON(t *testing.T) {
	ctx, err := newDiagnosticContext(
		[]string{"--diagnostic", "--json", "--org-id", reportOrg},
		&mockSystemInfoProvider{}, &mockDomainInfoProvider{},
		&mockServiceManager{}, &mockFileSystem{},
	)
	if err != nil || !ctx.JSON || ctx.Bundle {
		t.Fatalf("ctx %+v err %v", ctx, err)
	}
	if _, err := newDiagnosticContext(
		[]string{"--diagnostic", "--json", "--bundle"},
		&mockSystemInfoProvider{}, &mockDomainInfoProvider{},
		&mockServiceManager{}, &mockFileSystem{},
	); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Errorf("--json --bundle: %v", err)
	}
}
