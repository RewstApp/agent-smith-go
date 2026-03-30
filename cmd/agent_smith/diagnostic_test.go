package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
)

// ── test doubles ─────────────────────────────────────────────────────────────

type mockTLSDialer struct {
	result bool
	calls  []string // records "host:port" for each Dial call
}

func (m *mockTLSDialer) Dial(host, port string) bool {
	m.calls = append(m.calls, host+":"+port)
	return m.result
}

type mockLogFileOpener struct {
	content string
	err     error
}

func (m *mockLogFileOpener) Open(_ string) (io.ReadCloser, error) {
	if m.err != nil {
		return nil, m.err
	}
	return io.NopCloser(strings.NewReader(m.content)), nil
}

// ── scanAgentsFrom ────────────────────────────────────────────────────────────

func TestScanAgentsFrom_NonExistentRoot(t *testing.T) {
	agents := scanAgentsFrom("/does/not/exist/ever")
	if agents != nil {
		t.Errorf("expected nil for missing root, got %v", agents)
	}
}

func TestScanAgentsFrom_EmptyRoot(t *testing.T) {
	root := t.TempDir()
	agents := scanAgentsFrom(root)
	if len(agents) != 0 {
		t.Errorf("expected 0 agents, got %d", len(agents))
	}
}

func TestScanAgentsFrom_SkipsEntriesWithoutConfig(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "org-no-config"), 0o755)

	agents := scanAgentsFrom(root)
	if len(agents) != 0 {
		t.Errorf("expected 0 agents (no config.json), got %d", len(agents))
	}
}

func TestScanAgentsFrom_SkipsFiles(t *testing.T) {
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "somefile.json"), []byte("{}"), 0o644)

	agents := scanAgentsFrom(root)
	if len(agents) != 0 {
		t.Errorf("expected 0 agents (root-level files ignored), got %d", len(agents))
	}
}

func TestScanAgentsFrom_ValidConfig(t *testing.T) {
	root := t.TempDir()
	orgId := "test-org-123"
	orgDir := filepath.Join(root, orgId)
	_ = os.MkdirAll(orgDir, 0o755)

	device := agent.Device{
		DeviceId:        "device-abc",
		RewstOrgId:      orgId,
		AzureIotHubHost: "hub.example.com",
	}
	data, _ := json.Marshal(device)
	_ = os.WriteFile(filepath.Join(orgDir, "config.json"), data, 0o644)

	agents := scanAgentsFrom(root)
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}

	a := agents[0]
	if a.OrgId != orgId {
		t.Errorf("expected OrgId %q, got %q", orgId, a.OrgId)
	}
	if a.Device == nil {
		t.Fatal("expected Device to be populated")
	}
	if a.Device.DeviceId != "device-abc" {
		t.Errorf("expected DeviceId 'device-abc', got %q", a.Device.DeviceId)
	}
}

func TestScanAgentsFrom_InvalidConfigJSON(t *testing.T) {
	root := t.TempDir()
	orgId := "org-bad-json"
	orgDir := filepath.Join(root, orgId)
	_ = os.MkdirAll(orgDir, 0o755)
	_ = os.WriteFile(filepath.Join(orgDir, "config.json"), []byte("not-json"), 0o644)

	agents := scanAgentsFrom(root)
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent entry (even with bad JSON), got %d", len(agents))
	}
	if agents[0].Device != nil {
		t.Error("expected Device to be nil for invalid JSON")
	}
}

func TestScanAgentsFrom_MultipleOrgs(t *testing.T) {
	root := t.TempDir()
	for _, orgId := range []string{"org-a", "org-b", "org-c"} {
		orgDir := filepath.Join(root, orgId)
		_ = os.MkdirAll(orgDir, 0o755)
		data, _ := json.Marshal(agent.Device{DeviceId: orgId})
		_ = os.WriteFile(filepath.Join(orgDir, "config.json"), data, 0o644)
	}
	_ = os.MkdirAll(filepath.Join(root, "org-no-config"), 0o755)

	agents := scanAgentsFrom(root)
	if len(agents) != 3 {
		t.Errorf("expected 3 agents, got %d", len(agents))
	}
}

// ── runCheckAgents ────────────────────────────────────────────────────────────

func TestRunCheckAgents_Empty(t *testing.T) {
	params := &diagnosticContext{ServiceManager: &mockServiceManager{}}
	runCheckAgents(params, nil)
}

func TestRunCheckAgents_ServiceOpenFails(t *testing.T) {
	params := &diagnosticContext{
		ServiceManager: &mockServiceManager{openErr: errors.New("not found")},
	}
	runCheckAgents(params, []agentInfo{{OrgId: "org-1", ServiceName: "svc-1"}})
}

func TestRunCheckAgents_RunningService(t *testing.T) {
	params := &diagnosticContext{
		ServiceManager: &mockServiceManager{openService: &mockService{isActive: true}},
	}
	runCheckAgents(params, []agentInfo{{OrgId: "org-1", ServiceName: "svc-1"}})
}

func TestRunCheckAgents_StoppedService(t *testing.T) {
	params := &diagnosticContext{
		ServiceManager: &mockServiceManager{openService: &mockService{isActive: false}},
	}
	runCheckAgents(params, []agentInfo{{OrgId: "org-1", ServiceName: "svc-1"}})
}

func TestRunCheckAgents_WithDeviceDetails(t *testing.T) {
	params := &diagnosticContext{
		ServiceManager: &mockServiceManager{openService: &mockService{isActive: true}},
	}
	device := &agent.Device{
		DeviceId:        "dev-xyz",
		AzureIotHubHost: "hub.example.com",
		RewstEngineHost: "engine.example.com",
	}
	runCheckAgents(params, []agentInfo{{OrgId: "org-1", ServiceName: "svc-1", Device: device}})
}

// ── runConnectivityTestWith ───────────────────────────────────────────────────

func TestRunConnectivityTest_NoConfig(t *testing.T) {
	runConnectivityTestWith(agentInfo{OrgId: "org-1"}, &mockTLSDialer{})
}

func TestRunConnectivityTest_EmptyHost(t *testing.T) {
	target := agentInfo{OrgId: "org-1", Device: &agent.Device{AzureIotHubHost: ""}}
	runConnectivityTestWith(target, &mockTLSDialer{})
}

func TestRunConnectivityTest_BothFail(t *testing.T) {
	dialer := &mockTLSDialer{result: false}
	target := agentInfo{
		OrgId:  "org-1",
		Device: &agent.Device{AzureIotHubHost: "hub.example.com"},
	}
	runConnectivityTestWith(target, dialer)
	if len(dialer.calls) != 2 {
		t.Errorf("expected 2 dial attempts, got %d", len(dialer.calls))
	}
}

func TestRunConnectivityTest_BothSucceed(t *testing.T) {
	dialer := &mockTLSDialer{result: true}
	target := agentInfo{
		OrgId:  "org-1",
		Device: &agent.Device{AzureIotHubHost: "hub.example.com"},
	}
	runConnectivityTestWith(target, dialer)
	if len(dialer.calls) != 2 {
		t.Errorf("expected 2 dial attempts, got %d", len(dialer.calls))
	}
}

func TestTestTLSConnection_InvalidHost(t *testing.T) {
	if testTLSConnection("127.0.0.1", "19999") {
		t.Error("expected false for unreachable host, got true")
	}
}

// ── runTempDirTest ────────────────────────────────────────────────────────────

func TestRunTempDirTest_MissingDataDir(t *testing.T) {
	target := agentInfo{OrgId: "diag-test-" + t.Name()}
	runTempDirTest(target)
	_ = os.RemoveAll(agent.GetScriptsDirectory(target.OrgId))
}

// ── runLiveLogsWith ───────────────────────────────────────────────────────────

func TestRunLiveLogs_OpenError(t *testing.T) {
	opener := &mockLogFileOpener{err: errors.New("file not found")}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled — tail loop exits immediately
	runLiveLogsWith(ctx, agentInfo{OrgId: "org-1", LogFile: "/missing.log"}, opener)
}

func TestRunLiveLogs_SmallFile(t *testing.T) {
	content := "line1\nline2\nline3\n"
	opener := &mockLogFileOpener{content: content}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runLiveLogsWith(ctx, agentInfo{OrgId: "org-1", LogFile: "fake.log"}, opener)
}

// ── selectAgent ──────────────────────────────────────────────────────────────

func TestSelectAgent_ValidChoice(t *testing.T) {
	agents := []agentInfo{{OrgId: "org-a", IsRunning: true}, {OrgId: "org-b"}}
	result := selectAgent(bufio.NewReader(strings.NewReader("2\n")), agents)
	if result.OrgId != "org-b" {
		t.Errorf("expected 'org-b', got %q", result.OrgId)
	}
}

func TestSelectAgent_InvalidThenValid(t *testing.T) {
	agents := []agentInfo{{OrgId: "org-a"}, {OrgId: "org-b"}}
	result := selectAgent(bufio.NewReader(strings.NewReader("abc\n1\n")), agents)
	if result.OrgId != "org-a" {
		t.Errorf("expected 'org-a', got %q", result.OrgId)
	}
}

func TestSelectAgent_OutOfRangeThenValid(t *testing.T) {
	agents := []agentInfo{{OrgId: "org-a"}}
	result := selectAgent(bufio.NewReader(strings.NewReader("99\n1\n")), agents)
	if result.OrgId != "org-a" {
		t.Errorf("expected 'org-a', got %q", result.OrgId)
	}
}

// ── runCommandTest ────────────────────────────────────────────────────────────

func TestRunCommandTest_Success(t *testing.T) {
	runCommandTest()
}

// ── runDiagnosticWith (menu dispatch) ─────────────────────────────────────────

func newDiagnosticParams(orgId string) *diagnosticContext {
	return &diagnosticContext{
		OrgId:          orgId,
		ServiceManager: &mockServiceManager{openService: &mockService{isActive: true}},
	}
}

// runDiag is a helper that uses runDiagnosticFull with an empty temp root so
// tests are fully isolated from any real installed agents on the machine.
// It uses a short-lived context (2s) so live-log tail loops exit promptly.
func runDiag(t *testing.T, params *diagnosticContext, input string, dialer tlsDialer, opener logFileOpener) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	runDiagnosticFull(ctx, params, strings.NewReader(input), dialer, opener, t.TempDir())
}

func TestRunDiagnosticWith_ExitImmediately(t *testing.T) {
	// No installed agents, no org-id → prints "no agents" and returns without reading input
	runDiag(t,
		&diagnosticContext{ServiceManager: &mockServiceManager{}},
		"",
		&mockTLSDialer{},
		&mockLogFileOpener{},
	)
}

func TestRunDiagnosticWith_MenuExit(t *testing.T) {
	runDiag(t, newDiagnosticParams("org-1"), "0\n", &mockTLSDialer{}, &mockLogFileOpener{})
}

func TestRunDiagnosticWith_MenuQuit(t *testing.T) {
	runDiag(t, newDiagnosticParams("org-1"), "q\n", &mockTLSDialer{}, &mockLogFileOpener{})
}

func TestRunDiagnosticWith_Option1_ScanAgents(t *testing.T) {
	runDiag(t, newDiagnosticParams("org-1"), "1\n0\n", &mockTLSDialer{}, &mockLogFileOpener{})
}

func TestRunDiagnosticWith_Option2_CommandTest(t *testing.T) {
	runDiag(t, newDiagnosticParams("org-1"), "2\n0\n", &mockTLSDialer{}, &mockLogFileOpener{})
}

func TestRunDiagnosticWith_Option3_Connectivity(t *testing.T) {
	runDiag(t, newDiagnosticParams("org-1"), "3\n0\n", &mockTLSDialer{result: true}, &mockLogFileOpener{})
}

func TestRunDiagnosticWith_Option4_TempDir(t *testing.T) {
	params := newDiagnosticParams("org-diag-opt4")
	runDiag(t, params, "4\n0\n", &mockTLSDialer{}, &mockLogFileOpener{})
	_ = os.RemoveAll(agent.GetScriptsDirectory(params.OrgId))
}

func TestRunDiagnosticWith_Option5_LiveLogs(t *testing.T) {
	runDiag(t, newDiagnosticParams("org-1"), "5\n0\n", &mockTLSDialer{}, &mockLogFileOpener{content: "log line\n"})
}

func TestRunDiagnosticWith_Option6_AllChecks(t *testing.T) {
	params := newDiagnosticParams("org-diag-opt6")
	runDiag(t, params, "6\n0\n", &mockTLSDialer{result: true}, &mockLogFileOpener{content: "log\n"})
	_ = os.RemoveAll(agent.GetScriptsDirectory(params.OrgId))
}

func TestRunDiagnosticWith_InvalidOption(t *testing.T) {
	runDiag(t, newDiagnosticParams("org-1"), "99\n0\n", &mockTLSDialer{}, &mockLogFileOpener{})
}

func TestRunDiagnosticWith_AgentSelectionFromScan(t *testing.T) {
	// Two agents in the temp root → selectAgent is called; "1" picks first, "0" exits
	root := t.TempDir()
	for _, orgId := range []string{"org-a", "org-b"} {
		orgDir := filepath.Join(root, orgId)
		_ = os.MkdirAll(orgDir, 0o755)
		data, _ := json.Marshal(agent.Device{DeviceId: orgId, RewstOrgId: orgId})
		_ = os.WriteFile(filepath.Join(orgDir, "config.json"), data, 0o644)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	runDiagnosticFull(
		ctx,
		&diagnosticContext{ServiceManager: &mockServiceManager{openService: &mockService{isActive: true}}},
		strings.NewReader("1\n0\n"),
		&mockTLSDialer{},
		&mockLogFileOpener{},
		root,
	)
}

// ── runAllChecksWith ──────────────────────────────────────────────────────────

func TestRunAllChecksWith(t *testing.T) {
	params := &diagnosticContext{
		ServiceManager: &mockServiceManager{openService: &mockService{isActive: false}},
	}
	agents := []agentInfo{{OrgId: "org-1", ServiceName: "svc-1"}}
	target := agentInfo{
		OrgId:   "org-all",
		LogFile: "fake.log",
		Device:  &agent.Device{AzureIotHubHost: "hub.example.com"},
	}
	runAllChecksWith(params, agents, target, &mockTLSDialer{result: true}, &mockLogFileOpener{content: "log\n"})
	_ = os.RemoveAll(agent.GetScriptsDirectory(target.OrgId))
}
