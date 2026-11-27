package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"sort"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/mqtt"
	"github.com/RewstApp/agent-smith-go/internal/version"
)

// --diagnostic --json (sc-132994) runs the non-interactive checks and prints
// one JSON document, so an RMM script or a workflow can ask "is the agent on
// this endpoint healthy?" without scraping [PASS]/[FAIL] lines. Every check a
// section prints is a checkResult: the console line, the bundle's
// diagnostic.txt and the document are three renderings of the same result.

// The check names: the document's stable identifiers, documented in the
// README's "Scripting the diagnostic" section. A section that cannot run
// (no configuration, say) reports only the check that says why.
const (
	checkConfigLoad         = "config.load"
	checkServiceStatus      = "service.status"
	checkCommandExecution   = "command.execution"
	checkHubConfigured      = "hub.configured"
	checkHubProxyURL        = "hub.proxy_url"
	checkHubDirect8883      = "hub.direct_8883"
	checkHubDirect443       = "hub.direct_443"
	checkHubProxy443        = "hub.proxy_443"
	checkHubTransport       = "hub.transport"
	checkEngineConfigured   = "engine.configured"
	checkEngineProxyURL     = "engine.proxy_url"
	checkEngineDirect       = "engine.direct"
	checkEngineProxy        = "engine.proxy"
	checkEngineReachable    = "engine.reachable"
	checkEngineSpool        = "engine.spool"
	checkScriptsDirSecure   = "scripts_dir.secure"
	checkScriptsDirWrite    = "scripts_dir.write"
	checkScriptsDirReadBack = "scripts_dir.read_back"
	checkDataDirExists      = "data_dir.exists"
	checkFleetHealth        = "fleet_health.report"
	checkHostInfo           = "host.info"
	// checkLogOpen is the live log viewer's, which only the menu runs.
	checkLogOpen = "log.open"
)

// The exit codes of --diagnostic --json, so a script can branch without
// parsing: every check passed, the report could not be produced (a usage
// error exits 1 as well), or a check failed.
const (
	diagnosticExitPassed = 0
	diagnosticExitError  = 1
	diagnosticExitFailed = 2
)

// reportWithheld stands in for a running command's last output line in the
// document's health object: the report carries no command output.
const reportWithheld = "(withheld from the report)"

// checkResult is one check's outcome. Advisory marks a result that is
// evidence for another check's verdict rather than a verdict itself - a
// single path to the hub or the engine, when hub.transport and
// engine.reachable decide whether the agent gets through - so a direct port
// a proxy-only network blocks does not fail the report. Fields are the
// check's own facts, flattened into its JSON object after the common keys.
type checkResult struct {
	Name     string
	Section  string
	OK       bool
	Advisory bool
	Detail   string
	Fields   map[string]any
}

// checkKeys are the keys every result's object starts with; a field cannot
// replace one.
var checkKeys = map[string]bool{
	"name": true, "section": true, "ok": true, "advisory": true, "detail": true,
}

// MarshalJSON writes name, section, ok, advisory and detail first, then the
// fields in key order.
func (r checkResult) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	write := func(key string, value any) error {
		if buf.Len() > 1 {
			buf.WriteByte(',')
		}
		encoded, err := marshalJSON(map[string]any{key: value})
		if err != nil {
			return fmt.Errorf("check %s field %s: %w", r.Name, key, err)
		}
		// {"key":value} without its braces.
		buf.Write(encoded[1 : len(encoded)-1])
		return nil
	}
	common := []struct {
		key   string
		value any
	}{
		{"name", r.Name},
		{"section", r.Section},
		{"ok", r.OK},
		{"advisory", r.Advisory},
		{"detail", r.Detail},
	}
	for _, c := range common {
		if err := write(c.key, c.value); err != nil {
			return nil, err
		}
	}
	keys := make([]string, 0, len(r.Fields))
	for key := range r.Fields {
		if !checkKeys[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := write(key, r.Fields[key]); err != nil {
			return nil, err
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// marshalJSON encodes v without escaping <, > and &, which a URL or a
// command line in a detail would otherwise carry as < and friends.
func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// diagSection is the title of the section printing, recorded with each of its
// results.
var diagSection string

// diagChecks collects the results the sections report while recordChecks
// runs them; nil records nothing.
var diagChecks *[]checkResult

// report prints one check's [PASS]/[FAIL] line to diagOut and records it.
func report(r checkResult) {
	r.Section = diagSection
	if diagChecks != nil {
		*diagChecks = append(*diagChecks, r)
	}
	if r.OK {
		dprintf("    [PASS] %s\n", r.Detail)
	} else {
		dprintf("    [FAIL] %s\n", r.Detail)
	}
}

// recordChecks runs the sections and returns every result they reported.
func recordChecks(run func()) []checkResult {
	checks := []checkResult{}
	previous := diagChecks
	diagChecks = &checks
	defer func() { diagChecks = previous }()
	run()
	return checks
}

// printConfigCheck reports whether config.json loads as the agent's
// configuration. One that does not is the one fact the report may have been
// asked for, so it comes first rather than surfacing only as a connectivity
// check that found no host (sc-133003).
func printConfigCheck(configErr error) {
	printSection("Configuration")
	if configErr != nil {
		printResult(checkConfigLoad, false, fmt.Sprintf(
			"config.json does not load as the agent's configuration: %v", configErr,
		))
		return
	}
	printResult(checkConfigLoad, true, "config.json loads as the agent's configuration")
}

// runReportSections runs the checks the bundle captures into diagnostic.txt
// and the JSON report records, in one order, with every running command's
// last output line withheld from Fleet Health. It returns the health file it
// read, unmodified, or nil when there is none.
func runReportSections(
	ctx context.Context,
	params *diagnosticContext,
	target agentInfo,
	dialer tlsDialer,
	now time.Time,
	configErr error,
) *mqtt.ReportedProperties {
	printConfigCheck(configErr)
	runCheckServiceStatus(ctx, params, target)
	runCommandTest()
	runConnectivityTestWith(target, dialer)
	runEngineTestWith(target, dialer, now)
	runTempDirTest(target)
	props, err := readHealthFileFrom(target.DataDir)
	printFleetHealth(props, err, false)
	if err != nil {
		return nil
	}
	return props
}

// diagnosticDocument is what --diagnostic --json prints.
type diagnosticDocument struct {
	OrgId string `json:"org_id"`
	// AgentVersion is the version the running service last reported in its
	// health file; empty before its first report.
	AgentVersion string `json:"agent_version"`
	// DiagnosticVersion and Platform describe the binary that ran the checks.
	DiagnosticVersion string `json:"diagnostic_version"`
	Platform          string `json:"platform"`
	GeneratedAt       string `json:"generated_at"`
	// OK is whether every check that is not advisory passed; Failed names the
	// ones that did not.
	OK     bool          `json:"ok"`
	Failed []string      `json:"failed"`
	Checks []checkResult `json:"checks"`
	// Health is the health file the service last wrote, each running
	// command's last output line withheld; null before its first report.
	Health *mqtt.ReportedProperties `json:"health"`
	// Host is the host information the agent reports at install; null when
	// it could not be gathered.
	Host *agent.HostInfo `json:"host"`
}

// runDiagnosticJSON writes the document for the agent the parameters name (or
// the only one installed) to env.stdout and returns the exit code. An error
// means no document was written: the agent could not be found or its files
// could not be read.
func runDiagnosticJSON(
	ctx context.Context,
	params *diagnosticContext,
	env diagnosticEnv,
) (int, error) {
	if env.now == nil {
		env.now = time.Now
	}
	if env.stdout == nil {
		env.stdout = os.Stdout
	}
	target, err := diagnosticTarget(ctx, params, env, jsonRun)
	if err != nil {
		return diagnosticExitError, err
	}
	if err := jsonRun.readDataDir(target.DataDir); err != nil {
		return diagnosticExitError, err
	}
	configBytes, err := os.ReadFile(target.ConfigFile)
	if err != nil {
		return diagnosticExitError, fmt.Errorf("cannot read the agent's configuration: %w", err)
	}
	// The secret values are scrubbed from the document the way the bundle
	// scrubs its entries. A configuration that is not JSON has none to name,
	// and is reported by config.load rather than refused: it is a finding.
	_, secrets, _ := redactConfig(configBytes)
	var device agent.Device
	configErr := json.Unmarshal(configBytes, &device)
	target.Device = nil
	if configErr == nil {
		target.Device = &device
	}
	now := env.now().UTC()

	doc := diagnosticDocument{
		OrgId:             target.OrgId,
		DiagnosticVersion: version.Version,
		Platform:          runtime.GOOS + "/" + runtime.GOARCH,
		GeneratedAt:       now.Format(time.RFC3339),
		Failed:            []string{},
	}
	doc.Checks = recordChecks(func() {
		// The document is the only thing on stdout; the console rendering
		// of the same results is not wanted beside it.
		previous := diagOut
		diagOut = io.Discard
		defer func() { diagOut = previous }()
		doc.Health = runReportSections(ctx, params, target, env.dialer, now, configErr)
		doc.Host = runHostInfo(ctx, params, target)
	})
	if doc.Health != nil {
		withholdOutput(doc.Health, reportWithheld)
		doc.AgentVersion = doc.Health.AgentVersion
	}
	doc.OK = true
	for _, c := range doc.Checks {
		if !c.OK && !c.Advisory {
			doc.OK = false
			doc.Failed = append(doc.Failed, c.Name)
		}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return diagnosticExitError, fmt.Errorf("cannot encode the report: %w", err)
	}
	if _, err := env.stdout.Write(scrub(buf.Bytes(), secrets)); err != nil {
		return diagnosticExitError, fmt.Errorf("failed to write the report: %w", err)
	}
	if !doc.OK {
		return diagnosticExitFailed, nil
	}
	return diagnosticExitPassed, nil
}
