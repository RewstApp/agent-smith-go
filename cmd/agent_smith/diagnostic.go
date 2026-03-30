package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/version"
)

// tlsDialer abstracts TLS connectivity checks so tests can inject fakes.
type tlsDialer interface {
	Dial(host, port string) bool
}

type defaultTLSDialer struct{}

func (d *defaultTLSDialer) Dial(host, port string) bool {
	return testTLSConnection(host, port)
}

// logFileOpener abstracts os.Open so tests can inject an in-memory reader.
type logFileOpener interface {
	Open(name string) (io.ReadCloser, error)
}

type osLogFileOpener struct{}

func (o *osLogFileOpener) Open(name string) (io.ReadCloser, error) {
	return os.Open(name) // #nosec G304 - path comes from internal config
}

type agentInfo struct {
	OrgId       string
	ConfigFile  string
	LogFile     string
	ServiceName string
	IsRunning   bool
	Device      *agent.Device
}

func runDiagnostic(params *diagnosticContext) {
	runDiagnosticFull(
		context.Background(),
		params,
		os.Stdin,
		&defaultTLSDialer{},
		&osLogFileOpener{},
		getAgentDataRoot(),
	)
}

func runDiagnosticFull(
	ctx context.Context,
	params *diagnosticContext,
	input io.Reader,
	dialer tlsDialer,
	opener logFileOpener,
	agentRoot string,
) {
	reader := bufio.NewReader(input)

	printHeader()

	// Scan for installed agents
	agents := scanAgentsFrom(agentRoot)

	if len(agents) == 0 && params.OrgId == "" {
		fmt.Println("\n  No installed agents found.")
		fmt.Println("  Use --org-id <ORG_ID> --diagnostic to diagnose a specific organization.")
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
			agents = append(agents, agentInfo{
				OrgId:       params.OrgId,
				ConfigFile:  agent.GetConfigFilePath(params.OrgId),
				LogFile:     agent.GetLogFilePath(params.OrgId),
				ServiceName: formatServiceName(params.OrgId),
			})
		}
	}

	// Select agent to diagnose
	var target agentInfo
	if len(agents) == 1 {
		target = agents[0]
	} else {
		target = selectAgent(reader, agents)
	}

	for {
		printMenu()
		choice := prompt(reader, "  Select an option: ")

		switch strings.TrimSpace(choice) {
		case "1":
			runCheckAgents(params, agents)
		case "2":
			runCommandTest()
		case "3":
			runConnectivityTestWith(target, dialer)
		case "4":
			runTempDirTest(target)
		case "5":
			runLiveLogsWith(ctx, target, opener)
		case "6":
			runAllChecksWith(params, agents, target, dialer)
		case "0", "q", "quit", "exit":
			fmt.Println("\n  Exiting diagnostic mode.")
			return
		default:
			fmt.Println("\n  Invalid option. Please try again.")
		}
	}
}

func printHeader() {
	fmt.Println()
	fmt.Println("  ╔══════════════════════════════════════════════════╗")
	fmt.Println("  ║         Agent Smith Diagnostic Mode              ║")
	fmt.Printf("  ║         Version: %-31s ║\n", version.Version)
	fmt.Printf("  ║         Platform: %-30s ║\n", runtime.GOOS+"/"+runtime.GOARCH)
	fmt.Println("  ╚══════════════════════════════════════════════════╝")
}

func printMenu() {
	fmt.Println()
	fmt.Println("  ┌──────────────────────────────────────────────────┐")
	fmt.Println("  │  Diagnostic Options                              │")
	fmt.Println("  ├──────────────────────────────────────────────────┤")
	fmt.Println("  │  [1] Scan installed agents and check status      │")
	fmt.Println("  │  [2] Test command execution                      │")
	fmt.Println("  │  [3] Test MQTT/WebSocket connectivity            │")
	fmt.Println("  │  [4] Test temp directory write access            │")
	fmt.Println("  │  [5] View live log data                          │")
	fmt.Println("  │  [6] Run all checks                              │")
	fmt.Println("  │  [0] Exit                                        │")
	fmt.Println("  └──────────────────────────────────────────────────┘")
}

func prompt(reader *bufio.Reader, message string) string {
	fmt.Print(message)
	input, _ := reader.ReadString('\n')
	return strings.TrimSpace(input)
}

func selectAgent(reader *bufio.Reader, agents []agentInfo) agentInfo {
	fmt.Println("\n  Multiple agents found. Select one to diagnose:")
	fmt.Println()
	for i, a := range agents {
		status := "stopped"
		if a.IsRunning {
			status = "running"
		}
		fmt.Printf("  [%d] %s (%s)\n", i+1, a.OrgId, status)
	}
	fmt.Println()

	for {
		choice := prompt(reader, "  Select agent number: ")
		var idx int
		if _, err := fmt.Sscanf(choice, "%d", &idx); err == nil && idx >= 1 && idx <= len(agents) {
			return agents[idx-1]
		}
		fmt.Println("  Invalid selection. Please try again.")
	}
}

// scanAgentsFrom is the testable core of scanAgents.
func scanAgentsFrom(root string) []agentInfo {
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
		configPath := filepath.Join(root, orgId, "config.json")

		// Verify this is an agent directory by checking for config.json
		if _, err := os.Stat(configPath); err != nil {
			continue
		}

		info := agentInfo{
			OrgId:       orgId,
			ConfigFile:  agent.GetConfigFilePath(orgId),
			LogFile:     agent.GetLogFilePath(orgId),
			ServiceName: formatServiceName(orgId),
		}

		// Try to load config
		configBytes, err := os.ReadFile(configPath)
		if err == nil {
			var device agent.Device
			if json.Unmarshal(configBytes, &device) == nil {
				info.Device = &device
			}
		}

		// Check service status via platform-native query
		_, info.IsRunning = queryServiceStatus(info.ServiceName)

		agents = append(agents, info)
	}

	return agents
}

// ── Check 1: Scan agents and show status ──

func runCheckAgents(params *diagnosticContext, agents []agentInfo) {
	printSection("Installed Agents")

	if len(agents) == 0 {
		printResult(false, "No installed agents found")
		return
	}

	for _, a := range agents {
		// Re-check status
		svc, err := params.ServiceManager.Open(a.ServiceName)
		if err != nil {
			printResult(false, fmt.Sprintf("%s - service not found (%s)", a.OrgId, a.ServiceName))
			continue
		}
		running := svc.IsActive()
		_ = svc.Close()

		status := "STOPPED"
		if running {
			status = "RUNNING"
		}
		printResult(running, fmt.Sprintf("%s - %s (%s)", a.OrgId, status, a.ServiceName))

		// Show config details if available
		if a.Device != nil {
			fmt.Printf("      Device ID:    %s\n", a.Device.DeviceId)
			fmt.Printf("      IoT Hub:      %s\n", a.Device.AzureIotHubHost)
			fmt.Printf("      Engine Host:  %s\n", a.Device.RewstEngineHost)
			fmt.Printf("      Log Level:    %s\n", a.Device.LoggingLevel)
			fmt.Printf("      Syslog:       %v\n", a.Device.UseSyslog)
			fmt.Printf("      Auto-Updates: %v\n", !a.Device.DisableAutoUpdates)
		}
	}
}

// ── Check 2: Command execution test ──

func runCommandTest() {
	printSection("Command Execution Test")

	shell, args := getTestCommand()
	fmt.Printf("    Shell: %s\n", shell)
	fmt.Printf("    Command: %s %s\n", shell, strings.Join(args, " "))

	start := time.Now()
	cmd := exec.Command(shell, args...) // #nosec G204 - diagnostic tool uses known shell commands
	output, err := cmd.CombinedOutput()
	elapsed := time.Since(start)

	if err != nil {
		printResult(false, fmt.Sprintf("Command execution failed: %v", err))
		if len(output) > 0 {
			fmt.Printf("    Output: %s\n", strings.TrimSpace(string(output)))
		}
		return
	}

	printResult(true, fmt.Sprintf("Command executed successfully (%s)", elapsed))
	fmt.Printf("    Output: %s\n", strings.TrimSpace(string(output)))
}

// ── Check 3: MQTT/WebSocket connectivity ──

func runConnectivityTestWith(target agentInfo, dialer tlsDialer) {
	printSection("MQTT/WebSocket Connectivity")

	if target.Device == nil {
		printResult(false, "No config loaded - cannot determine MQTT host")
		fmt.Println("    Ensure the agent has been configured and config.json exists.")
		return
	}

	host := target.Device.AzureIotHubHost
	if host == "" {
		printResult(false, "Azure IoT Hub host not configured")
		return
	}

	fmt.Printf("    Host: %s\n", host)

	// Test MQTT port (8883)
	fmt.Printf("    Testing MQTT (TLS port 8883)... ")
	mqttOk := dialer.Dial(host, "8883")
	if mqttOk {
		fmt.Println("OK")
	} else {
		fmt.Println("FAILED")
	}
	printResult(mqttOk, fmt.Sprintf("MQTT TLS connection to %s:8883", host))

	// Test WebSocket port (443)
	fmt.Printf("    Testing WebSocket (port 443)... ")
	wsOk := dialer.Dial(host, "443")
	if wsOk {
		fmt.Println("OK")
	} else {
		fmt.Println("FAILED")
	}
	printResult(wsOk, fmt.Sprintf("WebSocket connection to %s:443", host))

	if !mqttOk && !wsOk {
		fmt.Println()
		fmt.Println("    Troubleshooting tips:")
		fmt.Println("    - Check firewall rules for outbound ports 8883 and 443")
		fmt.Println("    - Verify DNS resolution for", host)
		fmt.Println("    - Check proxy/VPN settings that may block connections")
	}
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

func runTempDirTest(target agentInfo) {
	printSection("Temp Directory Write Test")

	scriptsDir := agent.GetScriptsDirectory(target.OrgId)

	// Test creating the scripts directory
	fmt.Printf("    Scripts directory: %s\n", scriptsDir)

	err := os.MkdirAll(scriptsDir, 0o755)
	if err != nil {
		printResult(false, fmt.Sprintf("Cannot create scripts directory: %v", err))
		return
	}
	printResult(true, "Scripts directory created/exists")

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
	dataDir := agent.GetDataDirectory(target.OrgId)
	fmt.Printf("    Data directory: %s\n", dataDir)
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
	fmt.Printf("    Log file: %s\n", logFile)
	fmt.Println("    Press Ctrl+C to stop watching.")
	fmt.Println()

	rc, err := opener.Open(logFile)
	if err != nil {
		printResult(false, fmt.Sprintf("Cannot open log file: %v", err))
		fmt.Println(
			"    The agent may not have been started yet, or the log file path is incorrect.",
		)
		return
	}
	defer func() { _ = rc.Close() }()

	// Read all buffered content line by line
	reader := bufio.NewReader(rc)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			fmt.Print("    ", line)
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
				fmt.Print("    ", string(buf[:n]))
			}
			if err != nil {
				time.Sleep(500 * time.Millisecond)
			}
		}
	}
}

// ── Check 6: Run all checks ──

func runAllChecksWith(
	params *diagnosticContext,
	agents []agentInfo,
	target agentInfo,
	dialer tlsDialer,
) {
	runCheckAgents(params, agents)
	runCommandTest()
	runConnectivityTestWith(target, dialer)
	runTempDirTest(target)
}

// ── Formatting helpers ──

func printSection(title string) {
	fmt.Println()
	fmt.Printf("  ── %s ──\n", title)
	fmt.Println()
}

func printResult(pass bool, message string) {
	if pass {
		fmt.Printf("    [PASS] %s\n", message)
	} else {
		fmt.Printf("    [FAIL] %s\n", message)
	}
}
