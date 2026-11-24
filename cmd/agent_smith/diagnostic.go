package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/mqtt"
	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
	"github.com/hashicorp/go-hclog"
)

// diagOut is where every diagnostic section prints: the console, or the
// console and a buffer while --diagnostic --bundle captures the sections into
// the archive (sc-129690).
var diagOut io.Writer = os.Stdout

// dprintf, dprintln and dprint write to diagOut. A console write that fails
// has nowhere better to be reported, so the result is discarded here, once.
func dprintf(format string, a ...any) { _, _ = fmt.Fprintf(diagOut, format, a...) }
func dprintln(a ...any)               { _, _ = fmt.Fprintln(diagOut, a...) }
func dprint(a ...any)                 { _, _ = fmt.Fprint(diagOut, a...) }

// tlsDialer abstracts TLS connectivity checks so tests can inject fakes.
type tlsDialer interface {
	Dial(host, port string) bool
	// DialViaProxy opens a TLS connection to host:port through an HTTP proxy
	// with CONNECT, the way the agent's WebSocket transport does (sc-129691).
	DialViaProxy(proxy *url.URL, host, port string) bool
	// Head sends an HTTPS HEAD to rawURL, through proxy when it is not nil,
	// the way the postback client would, and returns the status the server
	// answered with. An error is a transport failure: any answer, a 4xx
	// included, proves the path is open (sc-132998).
	Head(proxy *url.URL, rawURL string) (int, error)
}

type defaultTLSDialer struct{}

func (d *defaultTLSDialer) Dial(host, port string) bool {
	return testTLSConnection(host, port)
}

func (d *defaultTLSDialer) DialViaProxy(proxy *url.URL, host, port string) bool {
	return testTLSConnectionViaProxy(proxy, host, port)
}

func (d *defaultTLSDialer) Head(proxy *url.URL, rawURL string) (int, error) {
	return headRequest(proxy, rawURL)
}

// logFileOpener abstracts os.Open so tests can inject an in-memory reader.
type logFileOpener interface {
	Open(name string) (io.ReadCloser, error)
}

// osLogFileOpener opens the real log file. Its Open lives in per-platform files:
// on Windows it must ask for FILE_SHARE_DELETE, which Go's os.Open does not, so
// that the agent can rename the log underneath a running viewer instead of the
// viewer's handle blocking every rotation for as long as it stays open.
type osLogFileOpener struct{}

// liveLogOutput is where the live log viewer writes the log's content and its
// own markers. A package variable rather than os.Stdout inline so a test can
// capture what the viewer printed and assert every line appears exactly once
// across a rotation.
var liveLogOutput io.Writer = os.Stdout

// diagnosticQueryTimeout bounds each query the diagnostic makes to the
// service manager - the installed-agent scan, the Windows SCM fallback scan,
// the Service Status check and the bundle's service.txt. Since sc-132999 they
// all go through the service package's Status and Describe, which bound
// their own shell-outs at the install paths' five minutes; that is longer
// than an interactive check or a bundle should wait, so the diagnostic passes
// this deadline in the context and a wedged SCM or a D-Bus stall fails one
// query instead of hanging the run (sc-129690).
const diagnosticQueryTimeout = 30 * time.Second

// queryService opens the agent's service and asks the service manager for its
// state, under diagnosticQueryTimeout. An error means the service is not
// registered or the manager did not answer.
func queryService(
	ctx context.Context,
	mgr service.ServiceManager,
	name string,
) (service.Status, error) {
	svc, err := mgr.Open(name)
	if err != nil {
		return service.Status{}, err
	}
	defer func() { _ = svc.Close() }()
	ctx, cancel := context.WithTimeout(ctx, diagnosticQueryTimeout)
	defer cancel()
	return svc.Status(ctx)
}

// serviceRunning is whether the service manager reports the agent's service
// running; a service it cannot find or answer for is not.
func serviceRunning(ctx context.Context, mgr service.ServiceManager, name string) bool {
	status, err := queryService(ctx, mgr, name)
	return err == nil && status.State == service.StateRunning
}

// describeService is the service manager's own account of the agent's
// service for the bundle's service.txt (sc-129690): what Describe renders, or
// why there is none.
func describeService(ctx context.Context, mgr service.ServiceManager, name string) string {
	svc, err := mgr.Open(name)
	if err != nil {
		return fmt.Sprintf("service %s is not registered: %v\n", name, err)
	}
	defer func() { _ = svc.Close() }()
	ctx, cancel := context.WithTimeout(ctx, diagnosticQueryTimeout)
	defer cancel()
	text, err := svc.Describe(ctx)
	if err != nil {
		return fmt.Sprintf("%s(error: %v)\n", text, err)
	}
	return text
}

// agentFallbackScan is how a platform finds installed agents when the data
// root lists none: the Windows SCM scan (fallbackScanAgents), nothing
// elsewhere.
type agentFallbackScan func(context.Context, string, service.ServiceManager) []agentInfo

type agentInfo struct {
	OrgId string
	// DataDir is the org's data directory under the root the agent was
	// scanned from, and ConfigFile and LogFile are inside it. Every check
	// that reads the agent's files (the directory check, the live log
	// viewer, Fleet Health, the bundle) resolves through these rather than
	// through agent.GetDataDirectory, so the agent the menu listed is the
	// one they read (sc-133000).
	DataDir     string
	ConfigFile  string
	LogFile     string
	ServiceName string
	IsRunning   bool
	Device      *agent.Device
}

// agentInfoIn describes the org's agent under root, whether or not it is
// installed there: the paths are where its files are if it is.
func agentInfoIn(root, orgId string) agentInfo {
	dataDir := filepath.Join(root, orgId)
	return agentInfo{
		OrgId:       orgId,
		DataDir:     dataDir,
		ConfigFile:  filepath.Join(dataDir, agent.ConfigFileName),
		LogFile:     filepath.Join(dataDir, agent.LogFileName),
		ServiceName: formatServiceName(orgId),
	}
}

// getAgentDataRoot is the directory the installed agents are scanned from:
// the same root the service and the installer derive each org's data
// directory from, so the scan cannot list an agent the checks then read from
// somewhere else (sc-133000).
func getAgentDataRoot() string {
	return agent.GetDataRoot()
}

func runDiagnostic(params *diagnosticContext) {
	if params.Bundle {
		if params.BundlePath == bundleStdoutPath {
			// The zip owns stdout: every section and the closing line go
			// to stderr (sc-132995).
			diagOut = os.Stderr
		}
		report, err := runDiagnosticBundle(context.Background(), params, bundleEnv{
			root:     getAgentDataRoot(),
			fallback: fallbackScanAgents,
			dialer:   &defaultTLSDialer{},
			stdout:   os.Stdout,
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "Diagnostic bundle failed:", err)
			os.Exit(1)
		}
		dprintln()
		dprintf("Diagnostic bundle written: %s\n", report)
		return
	}
	runDiagnosticFull(
		context.Background(),
		params,
		os.Stdin,
		&defaultTLSDialer{},
		&osLogFileOpener{},
		getAgentDataRoot(),
		fallbackScanAgents,
	)
}

func runDiagnosticFull(
	ctx context.Context,
	params *diagnosticContext,
	input io.Reader,
	dialer tlsDialer,
	opener logFileOpener,
	agentRoot string,
	fallback agentFallbackScan,
) {
	reader := bufio.NewReader(input)

	printHeader()

	// Scan for installed agents; fall back to platform-specific discovery
	// (e.g. Windows SCM) if the data-directory scan returns nothing.
	agents := scanAgentsFrom(ctx, agentRoot, params.ServiceManager)
	if len(agents) == 0 && fallback != nil {
		agents = fallback(ctx, agentRoot, params.ServiceManager)
	}

	if len(agents) == 0 && params.OrgId == "" {
		dprintln("\n  No installed agents found.")
		dprintln("  Use --org-id <ORG_ID> --diagnostic to diagnose a specific organization.")
		return
	}

	// If org-id was provided, filter or create a single-agent list
	if params.OrgId != "" {
		found := false
		for _, a := range agents {
			if a.OrgId == params.OrgId {
				found = true
				break
			}
		}
		if !found {
			agents = append(agents, agentInfoIn(agentRoot, params.OrgId))
		}
	}

	hasMultiple := len(agents) > 1

selectLoop:
	for {
		// Select agent to diagnose
		var target agentInfo
		if !hasMultiple {
			target = agents[0]
		} else {
			selected, ok := selectAgent(reader, agents)
			if !ok {
				dprintln("\n  Exiting diagnostic mode.")
				return
			}
			target = selected
		}

		for {
			printMenu(target, hasMultiple)
			choice := prompt(reader, "  Select an option: ")

			switch strings.TrimSpace(choice) {
			case "1":
				runCheckServiceStatus(ctx, params, target)
			case "2":
				runCommandTest()
			case "3":
				runConnectivityTestWith(target, dialer)
				runEngineTestWith(target, dialer, time.Now())
			case "4":
				runTempDirTest(target)
			case "5":
				runLiveLogsWith(ctx, target, opener)
			case "6":
				runAllChecksWith(ctx, params, target, dialer)
			case "7":
				runHostInfo(ctx, params, target)
			case "8":
				runFleetHealth(target)
			case "0", "q", "quit", "exit":
				if hasMultiple {
					continue selectLoop
				}
				dprintln("\n  Exiting diagnostic mode.")
				return
			default:
				dprintln("\n  Invalid option. Please try again.")
			}
		}
	}
}

func printHeader() {
	dprintln()
	dprintln("  ╔══════════════════════════════════════════════════╗")
	dprintln("  ║         Agent Smith Diagnostic Mode              ║")
	dprintf("  ║         Version: %-31s ║\n", version.Version)
	dprintf("  ║         Platform: %-30s ║\n", runtime.GOOS+"/"+runtime.GOARCH)
	dprintln("  ╚══════════════════════════════════════════════════╝")
}

func printMenu(target agentInfo, hasMultiple bool) {
	lastOption := "  │  [0] Exit                                        │"
	if hasMultiple {
		lastOption = "  │  [0] Back                                        │"
	}
	dprintln()
	dprintln("  ┌──────────────────────────────────────────────────┐")
	dprintln("  │  Diagnostic Options                              │")
	dprintln("  ├──────────────────────────────────────────────────┤")
	dprintln("  │  [1] Check service status                        │")
	dprintln("  │  [2] Test command execution                      │")
	dprintln("  │  [3] Test hub and engine connectivity            │")
	dprintln("  │  [4] Test temp directory write access            │")
	dprintln("  │  [5] View live log data                          │")
	dprintln("  │  [6] Run all checks                              │")
	dprintln("  │  [7] Show host information                       │")
	dprintln("  │  [8] Show fleet health (as reported to the twin) │")
	dprintln(lastOption)
	dprintln("  └──────────────────────────────────────────────────┘")
	dprintf("  Current agent: %s\n", target.OrgId)
}

// prompt reads one choice. When the input has ended (choices piped in have run
// out, or the terminal closed) it answers "0", which exits at every level of
// the menu, rather than returning an empty string forever: an empty choice is
// "Invalid option", and the menu would print itself in a loop until killed
// (sc-131253: the e2e suite pipes the menu choices in).
func prompt(reader *bufio.Reader, message string) string {
	dprint(message)
	input, err := reader.ReadString('\n')
	if err != nil && strings.TrimSpace(input) == "" {
		dprintln()
		return "0"
	}
	return strings.TrimSpace(input)
}

func selectAgent(reader *bufio.Reader, agents []agentInfo) (agentInfo, bool) {
	dprintln("\n  Multiple agents found. Select one to diagnose:")
	dprintln()
	for i, a := range agents {
		dprintf("  [%d] %s\n", i+1, a.OrgId)
	}
	dprintf("  [0] Exit\n")
	dprintln()

	for {
		choice := prompt(reader, "  Select agent number: ")
		if strings.TrimSpace(choice) == "0" {
			return agentInfo{}, false
		}
		var idx int
		if _, err := fmt.Sscanf(choice, "%d", &idx); err == nil && idx >= 1 && idx <= len(agents) {
			return agents[idx-1], true
		}
		dprintln("  Invalid selection. Please try again.")
	}
}

var uuidRE = regexp.MustCompile(
	`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`,
)

func isValidOrgId(s string) bool {
	return uuidRE.MatchString(s)
}

// scanAgentsFrom lists the agents installed under root: every directory
// named by an org id that holds a config.json, with whether the service
// manager reports its service running (sc-132999: the same answer the
// Service Status check gives, from the same query).
func scanAgentsFrom(ctx context.Context, root string, mgr service.ServiceManager) []agentInfo {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}

	var agents []agentInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		orgId := entry.Name()
		if !isValidOrgId(orgId) {
			continue
		}

		info := agentInfoIn(root, orgId)

		// Verify this is an agent directory by checking for config.json
		if _, err := os.Stat(info.ConfigFile); err != nil {
			continue
		}

		// Try to load config
		configBytes, err := os.ReadFile(info.ConfigFile)
		if err == nil {
			var device agent.Device
			if json.Unmarshal(configBytes, &device) == nil {
				info.Device = &device
			}
		}

		info.IsRunning = serviceRunning(ctx, mgr, info.ServiceName)

		agents = append(agents, info)
	}

	return agents
}

// ── Check 1: Service status for the selected agent ──

func runCheckServiceStatus(ctx context.Context, params *diagnosticContext, target agentInfo) {
	printSection("Service Status")

	status, err := queryService(ctx, params.ServiceManager, target.ServiceName)
	if err != nil {
		printResult(false, fmt.Sprintf("Service not found (%s): %v", target.ServiceName, err))
		return
	}

	state := strings.ToUpper(status.State.String())
	if status.Raw != "" && !strings.EqualFold(status.Raw, status.State.String()) {
		// The manager's own words when they say more than the
		// classification: systemd's "active (running)", the SCM's
		// "StopPending".
		state += ": " + status.Raw
	}
	printResult(
		status.State == service.StateRunning,
		fmt.Sprintf("%s - %s (%s)", target.OrgId, state, target.ServiceName),
	)
}

// ── Check 2: Command execution test ──

func runCommandTest() {
	printSection("Command Execution Test")

	shell, args := getTestCommand()
	dprintf("    Shell: %s\n", shell)
	dprintf("    Command: %s %s\n", shell, strings.Join(args, " "))

	start := time.Now()
	cmd := exec.Command(shell, args...) // #nosec G204 - diagnostic tool uses known shell commands
	output, err := cmd.CombinedOutput()
	elapsed := time.Since(start)

	if err != nil {
		printResult(false, fmt.Sprintf("Command execution failed: %v", err))
		if len(output) > 0 {
			dprintf("    Output: %s\n", strings.TrimSpace(string(output)))
		}
		return
	}

	printResult(true, fmt.Sprintf("Command executed successfully (%s)", elapsed))
	dprintf("    Output: %s\n", strings.TrimSpace(string(output)))
}

// ── Check 3: MQTT/WebSocket connectivity ──

func runConnectivityTestWith(target agentInfo, dialer tlsDialer) {
	printSection("MQTT/WebSocket Connectivity")

	if target.Device == nil {
		printResult(false, "No config loaded - cannot determine MQTT host")
		dprintln("    Ensure the agent has been configured and config.json exists.")
		return
	}

	host := target.Device.AzureIotHubHost
	if host == "" {
		printResult(false, "Azure IoT Hub host not configured")
		return
	}

	dprintf("    Host: %s\n", host)
	r := checkConnectivity(*target.Device, dialer)
	dprintf("    Proxy: %s\n", r.proxyLine())
	if r.proxyErr != nil {
		printResult(false, fmt.Sprintf("proxy_url is invalid and is ignored: %v", r.proxyErr))
	}
	printResult(r.direct8883, fmt.Sprintf("MQTT TLS connection to %s:8883 (direct)", host))
	printResult(r.direct443, fmt.Sprintf("WebSocket connection to %s:443 (direct)", host))
	if r.proxy != nil {
		printResult(
			r.proxied443,
			fmt.Sprintf("WebSocket connection to %s:443 through the proxy", host),
		)
	}
	if r.chosen == "" {
		printResult(false, "No transport reaches the hub")
		dprintln()
		dprintln("    Troubleshooting tips:")
		dprintln("    - Check firewall rules for outbound ports 8883 and 443")
		dprintln("    - Verify DNS resolution for", host)
		dprintln("    - Set proxy_url (--proxy-url on --update) if egress needs a proxy")
		return
	}
	printResult(true, "The agent connects with "+r.chosen)
}

// connectivityResult is what the connectivity check found: the proxy in
// effect and where it came from, each path's outcome, and the transport the
// agent would use - the first that works in the order the agent tries them.
type connectivityResult struct {
	proxy       *url.URL
	proxySource string
	proxyErr    error
	direct8883  bool
	direct443   bool
	proxied443  bool
	chosen      string
}

func (r connectivityResult) proxyLine() string {
	if r.proxy == nil {
		return "none"
	}
	return fmt.Sprintf("%s (from %s)", utils.DescribeProxy(r.proxy), r.proxySource)
}

// checkConnectivity tests direct 8883, direct 443 and, when a proxy applies,
// 443 through it (sc-129691), with the agent's own tunnel. The environment
// proxy is the one of the shell running --diagnostic, which may differ from
// the service's.
func checkConnectivity(device agent.Device, dialer tlsDialer) connectivityResult {
	var r connectivityResult
	host := device.AzureIotHubHost
	explicit := strings.TrimSpace(device.ProxyURL)
	proxy, err := utils.ResolveProxy(explicit)
	if err != nil {
		r.proxyErr = err
		explicit = ""
		proxy, _ = utils.ResolveProxy("")
	}
	wsBroker, _ := url.Parse(mqtt.WebSocketBrokerURL(host))
	r.proxy = mqtt.BrokerProxy(proxy, wsBroker)
	if r.proxy != nil {
		r.proxySource = "the environment of this shell"
		if explicit != "" {
			r.proxySource = "proxy_url"
		}
	}

	dprintf("    Testing MQTT (TLS port 8883, direct)... ")
	r.direct8883 = dialer.Dial(host, "8883")
	dprintln(okOrFailed(r.direct8883))
	dprintf("    Testing WebSocket (port 443, direct)... ")
	r.direct443 = dialer.Dial(host, "443")
	dprintln(okOrFailed(r.direct443))
	if r.proxy != nil {
		dprintf("    Testing WebSocket (port 443) through %s... ",
			utils.DescribeProxy(r.proxy),
		)
		r.proxied443 = dialer.DialViaProxy(r.proxy, host, "443")
		dprintln(okOrFailed(r.proxied443))
	}

	// The transport the agent would use: the first broker, in the agent's own
	// order (mqtt.Brokers), whose path worked.
	for _, raw := range mqtt.Brokers(host, proxy) {
		broker, err := url.Parse(raw)
		if err != nil {
			continue
		}
		via := mqtt.BrokerProxy(proxy, broker)
		ok := r.direct8883
		if broker.Scheme == "wss" {
			ok = r.direct443
			if via != nil {
				ok = r.proxied443
			}
		}
		if !ok {
			continue
		}
		r.chosen = mqtt.Transport(broker)
		if via != nil {
			r.chosen += " through " + utils.DescribeProxy(via)
		}
		break
	}
	return r
}

func okOrFailed(ok bool) string {
	if ok {
		return "OK"
	}
	return "FAILED"
}

// ── Check 3b: Engine connectivity ──

// enginePingPath is the postback path the engine check sends a HEAD to: a
// post id no workflow issues, so the engine answers (a 4xx is expected) and
// records nothing.
const enginePingPath = "/webhooks/custom/action/diagnostic"

// runEngineTestWith checks the agent can reach rewst_engine_host, where every
// command result is posted back (sc-132998): a hub-only firewall rule lets the
// commands in and every result spool, the exact case a technician runs the
// diagnostic for, and the hub check alone passed it. The spool's size is
// printed beside the result so "unreachable" and "results piling up" appear
// together.
func runEngineTestWith(target agentInfo, dialer tlsDialer, now time.Time) {
	printSection("Engine Connectivity")

	if target.Device == nil {
		printResult(false, "No config loaded - cannot determine the engine host")
		dprintln("    Ensure the agent has been configured and config.json exists.")
		return
	}
	if strings.TrimSpace(target.Device.RewstEngineHost) == "" {
		printResult(false, "Rewst engine host not configured")
		return
	}

	dprintf("    Host: %s\n", target.Device.RewstEngineHost)
	r := checkEngine(*target.Device, dialer)
	dprintf("    Proxy: %s\n", r.proxyLine())
	if r.proxyErr != nil {
		printResult(false, fmt.Sprintf("proxy_url is invalid and is ignored: %v", r.proxyErr))
	}
	printResult(r.direct, fmt.Sprintf("TLS connection to %s (direct)", r.hostPort))
	if r.proxy != nil {
		printResult(r.proxied, fmt.Sprintf("TLS connection to %s through the proxy", r.hostPort))
	}
	if r.headErr != nil {
		printResult(
			false,
			fmt.Sprintf("Engine unreachable at %s %s: %v", r.pingURL, r.via, r.headErr),
		)
	} else {
		printResult(
			true,
			fmt.Sprintf("Engine answers HTTP %d at %s %s", r.status, r.pingURL, r.via),
		)
	}
	spool := filepath.Join(target.DataDir, "postback_spool")
	waiting, summary := spoolSummary(spool, now)
	printResult(!waiting, "Postback spool: "+summary)
	if r.headErr == nil {
		return
	}
	dprintln()
	dprintln("    Troubleshooting tips:")
	dprintln("    - Every command result is posted to this host; until it is reachable they spool")
	dprintln("    - Check firewall and proxy rules for outbound HTTPS to", r.hostPort)
	dprintln("    - Verify DNS resolution for", r.host)
	dprintln("    - Set proxy_url (--proxy-url on --update) if egress needs a proxy")
}

// engineResult is what the engine check found: the proxy in effect, each
// path's handshake, and the HEAD's answer over the path the agent would use.
type engineResult struct {
	host     string
	hostPort string
	pingURL  string
	proxy    *url.URL
	proxyErr error
	source   string
	direct   bool
	proxied  bool
	// via names the path the HEAD took: "(direct)" or "through <proxy>".
	via     string
	status  int
	headErr error
}

func (r engineResult) proxyLine() string {
	if r.proxy == nil {
		return "none"
	}
	return fmt.Sprintf("%s (from %s)", utils.DescribeProxy(r.proxy), r.source)
}

// checkEngine resolves the proxy the way the postback client does (the
// explicit proxy_url, else this shell's environment), tests a TLS handshake
// to the engine directly and through the proxy, then sends a HEAD along the
// path the agent would use. rewst_engine_host may carry a port (the e2e
// fixture serves on 8443); the agent's own postback URL is https on it.
func checkEngine(device agent.Device, dialer tlsDialer) engineResult {
	r := engineResult{host: strings.TrimSpace(device.RewstEngineHost)}
	r.hostPort = r.host
	port := "443"
	if h, p, err := net.SplitHostPort(r.host); err == nil {
		r.host, port = h, p
	} else {
		r.hostPort = net.JoinHostPort(r.host, port)
	}
	r.pingURL = "https://" + device.RewstEngineHost + enginePingPath

	explicit := strings.TrimSpace(device.ProxyURL)
	proxy, err := utils.ResolveProxy(explicit)
	if err != nil {
		r.proxyErr = err
		explicit = ""
		proxy, _ = utils.ResolveProxy("")
	}
	if target, err := url.Parse(r.pingURL); err == nil {
		r.proxy = utils.ProxyFor(proxy, target)
	}
	if r.proxy != nil {
		r.source = "the environment of this shell"
		if explicit != "" {
			r.source = "proxy_url"
		}
	}

	dprintf("    Testing HTTPS (port %s, direct)... ", port)
	r.direct = dialer.Dial(r.host, port)
	dprintln(okOrFailed(r.direct))
	r.via = "(direct)"
	if r.proxy != nil {
		dprintf("    Testing HTTPS (port %s) through %s... ", port, utils.DescribeProxy(r.proxy))
		r.proxied = dialer.DialViaProxy(r.proxy, r.host, port)
		dprintln(okOrFailed(r.proxied))
		r.via = "through " + utils.DescribeProxy(r.proxy)
	}
	dprintf("    Testing HEAD %s %s... ", r.pingURL, r.via)
	r.status, r.headErr = dialer.Head(r.proxy, r.pingURL)
	if r.headErr != nil {
		dprintln("FAILED")
	} else {
		dprintf("HTTP %d\n", r.status)
	}
	return r
}

// spoolSummary describes the postback spool at dir: how many results are
// waiting and how old the oldest is. waiting is true when any result is.
func spoolSummary(dir string, now time.Time) (waiting bool, summary string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, "empty (nothing has ever spooled)"
		}
		return false, fmt.Sprintf("%s cannot be read: %v", dir, err)
	}
	count := 0
	var oldest time.Time
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var e struct {
			PostId    string    `json:"post_id"`
			CreatedAt time.Time `json:"created_at"`
		}
		if json.Unmarshal(raw, &e) != nil || e.PostId == "" {
			continue
		}
		count++
		if oldest.IsZero() || e.CreatedAt.Before(oldest) {
			oldest = e.CreatedAt
		}
	}
	if count == 0 {
		return false, "empty"
	}
	return true, fmt.Sprintf(
		"%d result(s) waiting to be posted back, the oldest for %s (%s)",
		count, age(now, oldest), dir,
	)
}

// headRequest sends one HEAD to rawURL through proxy (nil for direct) with
// the agent's own proxy transport, without following a redirect, and returns
// the status answered. Any answer proves the path is open; only a transport
// failure is an error.
func headRequest(proxy *url.URL, rawURL string) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	transport := utils.ProxyTransport(func(*http.Request) (*url.URL, error) {
		return proxy, nil
	})
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// testTLSConnectionViaProxy opens the agent's CONNECT tunnel through proxy to
// host:port (utils.DialTunnel) and completes a TLS handshake inside it.
func testTLSConnectionViaProxy(proxy *url.URL, host, port string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := utils.DialTunnel(ctx, proxy, net.JoinHostPort(host, port))
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	tunneled := tls.Client(conn, &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	return tunneled.HandshakeContext(ctx) == nil
}

func testTLSConnection(host, port string) bool {
	conn, err := tls.DialWithDialer(
		&net.Dialer{Timeout: 10 * time.Second},
		"tcp",
		net.JoinHostPort(host, port),
		&tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// ── Check 4: Temp directory write test ──

// runTempDirTest checks the scripts directory is writable and the agent's
// data directory (target.DataDir: the one it was scanned from, which the
// bundle reads from) exists.
func runTempDirTest(target agentInfo) {
	printSection("Temp Directory Write Test")
	dataDir := target.DataDir

	scriptsDir := agent.GetScriptsDirectory(target.OrgId)

	// Test creating the scripts directory
	dprintf("    Scripts directory: %s\n", scriptsDir)

	err := utils.EnsureSecureDir(scriptsDir)
	if err != nil {
		printResult(false, fmt.Sprintf("Cannot create scripts directory: %v", err))
		return
	}
	printResult(true, "Scripts directory created/exists with hardened permissions")

	// Test writing a temp file
	testFile := filepath.Join(scriptsDir, "diagnostic-test.tmp")
	testContent := []byte("agent_smith diagnostic test")
	err = os.WriteFile(testFile, testContent, 0o644)
	if err != nil {
		printResult(false, fmt.Sprintf("Cannot write to scripts directory: %v", err))
		return
	}
	printResult(true, "File write successful")

	// Test reading it back
	readBack, err := os.ReadFile(testFile)
	if err != nil {
		printResult(false, fmt.Sprintf("Cannot read back test file: %v", err))
	} else if string(readBack) != string(testContent) {
		printResult(false, "File content mismatch after read-back")
	} else {
		printResult(true, "File read-back verified")
	}

	// Clean up
	_ = os.Remove(testFile)

	// Also check the data directory
	dprintf("    Data directory: %s\n", dataDir)
	if info, err := os.Stat(dataDir); err != nil {
		printResult(false, fmt.Sprintf("Data directory does not exist: %v", err))
	} else if !info.IsDir() {
		printResult(false, "Data path exists but is not a directory")
	} else {
		printResult(true, "Data directory exists")
	}
}

// ── Check 5: Live log viewer ──

func runLiveLogsWith(ctx context.Context, target agentInfo, opener logFileOpener) {
	printSection("Live Log Viewer")

	logFile := target.LogFile
	dprintf("    Log file: %s\n", logFile)
	dprintln("    Press Ctrl+C to stop watching.")
	dprintln()

	rc, err := opener.Open(logFile)
	if err != nil {
		printResult(false, fmt.Sprintf("Cannot open log file: %v", err))
		dprintln("    The agent may not have been started yet, or the log file path is incorrect.")
		return
	}
	defer func() { _ = rc.Close() }()

	// Read all buffered content line by line
	reader := bufio.NewReader(rc)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			_, _ = fmt.Fprint(liveLogOutput, "    ", line)
		}
		if err != nil {
			break
		}
	}

	// Tail for new entries until context is cancelled
	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			n, err := rc.Read(buf)
			if n > 0 {
				_, _ = fmt.Fprint(liveLogOutput, "    ", string(buf[:n]))
			}
			if err != nil {
				// At EOF, check whether the agent has rotated the log underneath
				// us: our handle would otherwise keep following the renamed
				// rewst_agent.log.1 forever while new lines land in a fresh file
				// at the original path. The switch drains the old file first, so
				// a line the agent wrote to it between this EOF and the rename is
				// printed rather than skipped.
				if logRotated(rc, logFile) {
					if next, ok := followRotatedLog(rc, logFile, opener, liveLogOutput); ok {
						rc = next
						continue
					}
				}
				time.Sleep(500 * time.Millisecond)
			}
		}
	}
}

// followRotatedLog moves the viewer from the rotated file it holds open to the
// new active file at path. It first reads the old handle to EOF and prints
// whatever is there - the agent may have written lines to the old file after
// the viewer's last read and before the rename, and swapping without draining
// would silently skip them - then opens the new file. If the new file cannot be
// opened yet (the rename has happened but the reopen has not), the old handle
// is kept and false is returned so the caller retries on its next poll; nothing
// is closed until its replacement is in hand.
func followRotatedLog(
	old io.ReadCloser,
	path string,
	opener logFileOpener,
	out io.Writer,
) (io.ReadCloser, bool) {
	buf := make([]byte, 4096)
	for {
		n, err := old.Read(buf)
		if n > 0 {
			_, _ = fmt.Fprint(out, "    ", string(buf[:n]))
		}
		if err != nil {
			break
		}
	}

	next, err := opener.Open(path)
	if err != nil {
		return old, false
	}
	_ = old.Close()
	// Best effort: the viewer is a display, and a console write error is not
	// something it can act on.
	_, _ = fmt.Fprintln(out, "    --- log rotated; now following the new file ---")
	return next, true
}

// logRotated reports whether the file open behind rc is no longer the file at
// path - i.e. the agent rotated the log by renaming it and reopened a new one.
// Identity is compared with os.SameFile, which is inode-based on Unix and
// file-index-based on Windows, so it is not fooled by a new file that has
// already grown past the old one's size. A reader that is not an *os.File (the
// tests' in-memory opener) can never be rotated underneath and reports false;
// so does a transient stat failure, which the next poll retries.
func logRotated(rc io.ReadCloser, path string) bool {
	f, ok := rc.(*os.File)
	if !ok {
		return false
	}
	open, err := f.Stat()
	if err != nil {
		return false
	}
	current, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !os.SameFile(open, current)
}

// ── Check 6: Run all checks ──

func runAllChecksWith(
	ctx context.Context,
	params *diagnosticContext,
	target agentInfo,
	dialer tlsDialer,
) {
	runCheckServiceStatus(ctx, params, target)
	runCommandTest()
	runConnectivityTestWith(target, dialer)
	runEngineTestWith(target, dialer, time.Now())
	runTempDirTest(target)
	runHostInfo(ctx, params, target)
	runFleetHealth(target)
}

// ── Check 8: Fleet health ──

// runFleetHealth prints the fleet-health object the service last published to
// the device twin (sc-129687), read from the file it leaves beside the
// journal in the directory the agent was scanned from, so what the machine
// says can be compared with what Rewst sees.
func runFleetHealth(target agentInfo) {
	props, err := readHealthFileFrom(target.DataDir)
	printFleetHealth(props, err, true)
}

// printFleetHealth prints the Fleet Health section from a health file read
// (props, err). withOutput false withholds each running command's last output
// line: the diagnostic bundle carries no command output (sc-129690).
func printFleetHealth(props *mqtt.ReportedProperties, err error, withOutput bool) {
	printSection("Fleet Health")
	if err != nil {
		printResult(
			false,
			fmt.Sprintf(
				"No fleet-health report yet (%v); the service writes one after it subscribes",
				err,
			),
		)
		return
	}
	health := props.Health
	printResult(
		true,
		fmt.Sprintf(
			"Reported at %s, up %ds, connected at %q, last connection error %q",
			health.ReportedAt,
			health.UptimeSeconds,
			health.ConnectedAt,
			health.LastConnectionError,
		),
	)
	dprintf("    journal: pending=%d degraded=%t expired_total=%d interrupted_total=%d\n",
		health.Journal.Pending,
		health.Journal.Degraded,
		health.Journal.ExpiredTotal,
		health.Journal.InterruptedTotal,
	)
	dprintf("    spool: entries=%d dropped expired=%d capacity=%d attempts=%d corrupt=%d\n",
		health.Spool.Entries,
		health.Spool.DroppedExpired,
		health.Spool.DroppedCapacity,
		health.Spool.DroppedAttempts,
		health.Spool.DroppedCorrupt,
	)
	dprintf("    commands: executing=%d completed_total=%d timed_out_total=%d failed_total=%d\n",
		health.Commands.Executing,
		health.Commands.CompletedTotal,
		health.Commands.TimedOutTotal,
		health.Commands.FailedTotal,
	)
	if p := health.Process; p != nil {
		// What the agent process costs the endpoint (sc-132992), as of the
		// last report: open_files on Linux and macOS, handles on Windows.
		descriptors := fmt.Sprintf("open_files=%d", p.OpenFiles)
		if p.Handles != 0 {
			descriptors = fmt.Sprintf("handles=%d", p.Handles)
		}
		dprintf(
			"    process: rss_bytes=%d (%.1f MiB) cpu_seconds=%.2f goroutines=%d threads=%d %s\n",
			p.RSSBytes, float64(p.RSSBytes)/(1<<20), p.CPUSeconds, p.Goroutines, p.Threads,
			descriptors,
		)
	} else {
		dprintln("    process: not in this report (written by an agent that does not sample it)")
	}
	printUpdateHealth(health.Update)
	if len(health.Plugins) == 0 {
		dprintln("    plugins: none")
	}
	for _, p := range health.Plugins {
		dprintf("    plugin %s: restarts=%d notify_failures=%d notify_timeouts=%d\n",
			p.Name, p.Restarts, p.NotifyFailures, p.NotifyTimeouts)
	}
	if props.Commands == nil || len(props.Commands.InFlight) == 0 {
		dprintln("    in flight: none at the last report")
	}
	for _, c := range inFlightOrNone(props.Commands) {
		last := c.LastOutputLine
		if !withOutput && last != "" {
			last = bundleWithheld
		}
		dprintf(
			"    in flight %s: started %s, elapsed %ds, stdout %d B, stderr %d B, last line %q\n",
			c.PostID,
			c.StartedAt,
			c.ElapsedSeconds,
			c.StdoutBytes,
			c.StderrBytes,
			last,
		)
	}
}

// printUpdateHealth prints the auto-updater's last finding (sc-132993): a
// newer release, the window it waits for and when that opens.
func printUpdateHealth(u *mqtt.UpdateHealth) {
	if u == nil {
		dprintln("    update: not checked yet, no update window")
		return
	}
	line := fmt.Sprintf("    update: available=%t", u.Available)
	if u.Version != "" {
		line += " version=" + u.Version
	}
	window := u.Window
	if window == "" {
		window = "any time"
	}
	line += fmt.Sprintf(" window=%q", window)
	if u.WindowOpensAt != "" {
		line += " waiting until " + u.WindowOpensAt
	}
	if u.CheckedAt != "" {
		line += " checked_at=" + u.CheckedAt
	} else {
		line += " (not checked yet)"
	}
	dprintln(line)
}

// ── Check 7: Host information ──

func runHostInfo(ctx context.Context, params *diagnosticContext, target agentInfo) {
	printSection("Host Information")

	if params.Sys == nil || params.Domain == nil {
		printResult(false, "System or domain provider not available")
		return
	}

	info, err := agent.NewHostInfo(
		ctx,
		target.OrgId,
		hclog.NewNullLogger(),
		params.Sys,
		params.Domain,
	)
	if err != nil {
		printResult(false, fmt.Sprintf("Failed to gather host information: %v", err))
		return
	}

	printResult(true, "Host information collected")
	dprintf("      Hostname:                 %s\n", strOrNA(info.HostName))
	dprintf("      MAC Address:              %s\n", ptrOrNA(info.MacAddress))
	dprintf("      Operating System:         %s\n", strOrNA(info.OperatingSystem))
	dprintf("      CPU Model:                %s\n", strOrNA(info.CpuModel))
	dprintf("      RAM (GB):                 %s\n", strOrNA(info.RamGb))
	dprintf("      Agent Version:            %s\n", strOrNA(info.AgentVersion))
	dprintf("      Agent Executable Path:    %s\n", strOrNA(info.AgentExecutablePath))
	dprintf("      Service Executable Path:  %s\n",
		strOrNA(info.ServiceExecutablePath),
	)
	dprintf("      AD Domain:                %s\n", ptrOrNA(info.AdDomain))
	dprintf("      AD Domain Controller:     %v\n", info.IsAdDomainController)
	dprintf("      Entra Connect Server:     %v\n", info.IsEntraConnectServer)
	dprintf("      Entra Domain:             %s\n", ptrOrNA(info.EntraDomain))
	dprintf("      Org ID:                   %s\n", strOrNA(info.OrgId))
}

func strOrNA(s string) string {
	if s == "" {
		return "N/A"
	}
	return s
}

func ptrOrNA(s *string) string {
	if s == nil || *s == "" {
		return "N/A"
	}
	return *s
}

// ── Formatting helpers ──

func printSection(title string) {
	dprintln()
	dprintf("  ── %s ──\n", title)
	dprintln()
}

func printResult(pass bool, message string) {
	if pass {
		dprintf("    [PASS] %s\n", message)
	} else {
		dprintf("    [FAIL] %s\n", message)
	}
}
