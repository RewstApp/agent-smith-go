//go:build linux

package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mockSystemCtl

type mockSystemCtl struct {
	runCalls       [][]string
	runErrOnCmd    string // if set, only fail when args[0] matches
	runErr         error
	configFilePath string
	// output is what Output prints; outputErr fails it. Output records its
	// calls in runCalls as well.
	output    []byte
	outputErr error
}

func (m *mockSystemCtl) Run(args ...string) error {
	m.runCalls = append(m.runCalls, args)
	if m.runErrOnCmd != "" {
		if len(args) > 0 && args[0] == m.runErrOnCmd {
			return m.runErr
		}
		return nil
	}
	return m.runErr
}

func (m *mockSystemCtl) Output(_ context.Context, args ...string) ([]byte, error) {
	m.runCalls = append(m.runCalls, args)
	return m.output, m.outputErr
}

func (m *mockSystemCtl) ServiceConfigFilePath(name string) string {
	return m.configFilePath
}

// helpers

func newTempConfigPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "test.service")
}

// defaultSystemCtl tests (real exec.Command, not the mock)

func TestResolveSystemctlTimeout(t *testing.T) {
	orig := systemctlTimeoutOverrideStr
	t.Cleanup(func() { systemctlTimeoutOverrideStr = orig })

	systemctlTimeoutOverrideStr = ""
	if got := resolveSystemctlTimeout(); got != systemctlTimeout {
		t.Errorf("expected default %v, got %v", systemctlTimeout, got)
	}

	systemctlTimeoutOverrideStr = "25s"
	if got := resolveSystemctlTimeout(); got != 25*time.Second {
		t.Errorf("expected overridden 25s, got %v", got)
	}

	for _, bad := range []string{"not-a-duration", "-5s", "0s"} {
		systemctlTimeoutOverrideStr = bad
		if got := resolveSystemctlTimeout(); got != systemctlTimeout {
			t.Errorf("expected default for invalid override %q, got %v", bad, got)
		}
	}
}

func TestDefaultSystemCtl_Run_Success(t *testing.T) {
	s := &defaultSystemCtl{binary: "/bin/echo"}

	if err := s.Run("hello"); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestDefaultSystemCtl_Run_TimesOutOnHungCommand(t *testing.T) {
	orig := systemctlTimeoutOverrideStr
	systemctlTimeoutOverrideStr = "200ms"
	t.Cleanup(func() { systemctlTimeoutOverrideStr = orig })

	s := &defaultSystemCtl{binary: "/bin/sleep"}

	start := time.Now()
	done := make(chan struct{})
	var err error
	go func() {
		err = s.Run("30")
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return; the hung command was not killed by the timeout")
	}

	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Errorf("Run took %v to be killed; expected roughly the overridden 200ms timeout", elapsed)
	}
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected error to mention timeout, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "30") {
		t.Errorf("expected error to name the command args, got %q", err.Error())
	}
}

// linuxService tests

func TestLinuxService_Close(t *testing.T) {
	svc := &linuxService{name: "test-svc", system: &mockSystemCtl{}}
	if err := svc.Close(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestLinuxService_Start(t *testing.T) {
	mock := &mockSystemCtl{}
	svc := &linuxService{name: "test-svc", system: mock}

	if err := svc.Start(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(mock.runCalls) != 1 || mock.runCalls[0][0] != "start" ||
		mock.runCalls[0][1] != "test-svc" {
		t.Errorf("expected Run(start, test-svc), got %v", mock.runCalls)
	}
}

func TestLinuxService_Start_Error(t *testing.T) {
	mock := &mockSystemCtl{runErr: errors.New("start failed")}
	svc := &linuxService{name: "test-svc", system: mock}

	if err := svc.Start(); err == nil {
		t.Error("expected error, got nil")
	}
}

func TestLinuxService_Stop(t *testing.T) {
	mock := &mockSystemCtl{}
	svc := &linuxService{name: "test-svc", system: mock}

	if err := svc.Stop(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(mock.runCalls) != 1 || mock.runCalls[0][0] != "stop" ||
		mock.runCalls[0][1] != "test-svc" {
		t.Errorf("expected Run(stop, test-svc), got %v", mock.runCalls)
	}
}

func TestLinuxService_Stop_Error(t *testing.T) {
	mock := &mockSystemCtl{runErr: errors.New("stop failed")}
	svc := &linuxService{name: "test-svc", system: mock}

	if err := svc.Stop(); err == nil {
		t.Error("expected error, got nil")
	}
}

func TestLinuxService_Delete(t *testing.T) {
	tmpFile := newTempConfigPath(t)
	if err := os.WriteFile(tmpFile, []byte{}, 0o600); err != nil {
		t.Fatal(err)
	}
	mock := &mockSystemCtl{configFilePath: tmpFile}
	svc := &linuxService{name: "test-svc", system: mock}

	if err := svc.Delete(); err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(mock.runCalls) != 1 || mock.runCalls[0][0] != "disable" ||
		mock.runCalls[0][1] != "test-svc" {
		t.Errorf("expected Run(disable, test-svc), got %v", mock.runCalls)
	}
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Error("expected config file to be removed")
	}
}

func TestLinuxService_Delete_DisableError(t *testing.T) {
	mock := &mockSystemCtl{runErr: errors.New("disable failed")}
	svc := &linuxService{name: "test-svc", system: mock}

	if err := svc.Delete(); err == nil {
		t.Error("expected error, got nil")
	}
}

func TestLinuxService_Delete_RemoveError(t *testing.T) {
	mock := &mockSystemCtl{configFilePath: "/nonexistent/path/file.service"}
	svc := &linuxService{name: "test-svc", system: mock}

	if err := svc.Delete(); err == nil {
		t.Error("expected error removing nonexistent file, got nil")
	}
}

func TestLinuxService_IsActive_Active(t *testing.T) {
	mock := &mockSystemCtl{
		output: []byte("LoadState=loaded\nActiveState=active\nSubState=running\n"),
	}
	svc := &linuxService{name: "test-svc", system: mock}

	if !svc.IsActive() {
		t.Error("expected IsActive to return true when the unit is active")
	}
	want := []string{"show", "test-svc", "--property=LoadState,ActiveState,SubState", "--no-pager"}
	if len(mock.runCalls) != 1 || strings.Join(mock.runCalls[0], " ") != strings.Join(want, " ") {
		t.Errorf("IsActive ran %v, want %v", mock.runCalls, want)
	}
}

func TestLinuxService_IsActive_Inactive(t *testing.T) {
	mock := &mockSystemCtl{
		output: []byte("LoadState=loaded\nActiveState=inactive\nSubState=dead\n"),
	}
	svc := &linuxService{name: "test-svc", system: mock}

	if svc.IsActive() {
		t.Error("expected IsActive to return false when the unit is inactive")
	}
}

func TestLinuxService_IsActive_QueryFails(t *testing.T) {
	mock := &mockSystemCtl{outputErr: errors.New("dbus stalled")}
	svc := &linuxService{name: "test-svc", system: mock}

	if svc.IsActive() {
		t.Error("expected IsActive to return false when Output fails")
	}
}

// Status (sc-132999)

func TestLinuxService_Status_ClassifiesActiveState(t *testing.T) {
	cases := []struct {
		active, sub string
		want        State
		raw         string
	}{
		{"active", "running", StateRunning, "active (running)"},
		{"inactive", "dead", StateStopped, "inactive (dead)"},
		{"failed", "failed", StateStopped, "failed (failed)"},
		{"activating", "auto-restart", StatePending, "activating (auto-restart)"},
		{"deactivating", "stop-sigterm", StatePending, "deactivating (stop-sigterm)"},
		{"reloading", "", StatePending, "reloading"},
		{"maintenance", "", StateUnknown, "maintenance"},
	}
	for _, tc := range cases {
		out := "LoadState=loaded\nActiveState=" + tc.active + "\n"
		if tc.sub != "" {
			out += "SubState=" + tc.sub + "\n"
		}
		svc := &linuxService{name: "test-svc", system: &mockSystemCtl{output: []byte(out)}}
		got, err := svc.Status(context.Background())
		if err != nil {
			t.Fatalf("%s: %v", tc.active, err)
		}
		if got.State != tc.want || got.Raw != tc.raw {
			t.Errorf("ActiveState=%s SubState=%s: got %v %q, want %v %q",
				tc.active, tc.sub, got.State, got.Raw, tc.want, tc.raw)
		}
	}
}

func TestLinuxService_Status_UnknownUnitIsAnError(t *testing.T) {
	mock := &mockSystemCtl{
		output: []byte("LoadState=not-found\nActiveState=inactive\nSubState=dead\n"),
	}
	svc := &linuxService{name: "test-svc", system: mock}

	_, err := svc.Status(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not-found") {
		t.Errorf("want an error naming LoadState=not-found, got %v", err)
	}
}

func TestLinuxService_Status_QueryError(t *testing.T) {
	mock := &mockSystemCtl{outputErr: errors.New("dbus stalled")}
	svc := &linuxService{name: "test-svc", system: mock}

	_, err := svc.Status(context.Background())
	if err == nil || !strings.Contains(err.Error(), "dbus stalled") {
		t.Errorf("want the query's error, got %v", err)
	}
}

// Describe (sc-132999)

func TestLinuxService_Describe_RecordsStatusAndShow(t *testing.T) {
	mock := &mockSystemCtl{output: []byte("stub unit output\n")}
	svc := &linuxService{name: "test-svc", system: mock}

	got, err := svc.Describe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := "$ systemctl status test-svc --no-pager --full\nstub unit output\n\n" +
		"$ systemctl show test-svc --no-pager\nstub unit output\n\n"
	if got != want {
		t.Errorf("Describe =\n%q\nwant\n%q", got, want)
	}
}

func TestLinuxService_Describe_KeepsAFailedQuerysOutput(t *testing.T) {
	// A stopped unit's `systemctl status` exits 3 with the output the support
	// case wants: the exit is recorded beside it, never in place of it.
	mock := &mockSystemCtl{
		output:    []byte("inactive (dead)\n"),
		outputErr: errors.New("exit status 3"),
	}
	svc := &linuxService{name: "test-svc", system: mock}

	got, err := svc.Describe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "inactive (dead)\n(exit: exit status 3)\n") {
		t.Errorf("Describe did not keep the output with its exit:\n%s", got)
	}
}

func TestLinuxService_Describe_TimeoutIsAnError(t *testing.T) {
	mock := &mockSystemCtl{output: []byte("partial"), outputErr: context.DeadlineExceeded}
	svc := &linuxService{name: "test-svc", system: mock}

	got, err := svc.Describe(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("want the deadline error, got %v", err)
	}
	if !strings.Contains(got, "partial") {
		t.Errorf("want what the query printed before the deadline, got %q", got)
	}
}

func TestDefaultSystemCtl_Output_CallerDeadlineBoundsTheCommand(t *testing.T) {
	s := &defaultSystemCtl{binary: "/bin/sleep"}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := s.Output(ctx, "30")
	if time.Since(start) > 5*time.Second {
		t.Errorf("Output ran %v past the caller's 200ms deadline", time.Since(start))
	}
	if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "timed out") {
		t.Errorf("want a timeout error wrapping the deadline, got %v", err)
	}
}

func TestDefaultSystemCtl_Output_NonZeroExitKeepsTheOutput(t *testing.T) {
	s := &defaultSystemCtl{binary: "/bin/sh"}

	out, err := s.Output(context.Background(), "-c", "echo printed; exit 3")
	if err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Errorf("want exec's exit error, got %v", err)
	}
	if !strings.Contains(string(out), "printed") {
		t.Errorf("want the output with the error, got %q", out)
	}
}

// NewServiceManager tests

func TestNewServiceManager_ReturnsNonNil(t *testing.T) {
	sm := NewServiceManager()
	if sm == nil {
		t.Fatal("expected non-nil ServiceManager")
	}
}

// defaultServiceManager.Create tests

func TestDefaultServiceManager_Create_Success(t *testing.T) {
	tmpFile := newTempConfigPath(t)
	mock := &mockSystemCtl{configFilePath: tmpFile}
	sm := &defaultServiceManager{system: mock}
	params := AgentParams{
		Name:                "test-svc",
		AgentExecutablePath: "/usr/bin/agent",
		OrgId:               "org-123",
		ConfigFilePath:      "/etc/agent/config.json",
		LogFilePath:         "/var/log/agent.log",
	}

	svc, err := sm.Create(params)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if svc == nil {
		t.Fatal("expected service, got nil")
	}
	if len(mock.runCalls) != 2 {
		t.Fatalf("expected 2 Run calls, got %d", len(mock.runCalls))
	}
	if mock.runCalls[0][0] != "daemon-reload" {
		t.Errorf("expected first Run call to be daemon-reload, got %v", mock.runCalls[0])
	}
	if mock.runCalls[1][0] != "enable" || mock.runCalls[1][1] != "test-svc" {
		t.Errorf("expected second Run call to be enable test-svc, got %v", mock.runCalls[1])
	}
}

func TestDefaultServiceManager_Create_ServiceConfigContent(t *testing.T) {
	tmpFile := newTempConfigPath(t)
	mock := &mockSystemCtl{configFilePath: tmpFile}
	sm := &defaultServiceManager{system: mock}
	params := AgentParams{
		Name:                "my-service",
		AgentExecutablePath: "/usr/bin/agent",
		OrgId:               "org-abc",
		ConfigFilePath:      "/etc/agent.json",
		LogFilePath:         "/var/log/agent.log",
	}

	if _, err := sm.Create(params); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}
	content := string(data)

	checks := []string{
		"[Unit]",
		"Description=my-service",
		"[Service]",
		"ExecStart=/usr/bin/agent --org-id org-abc --config-file /etc/agent.json --log-file /var/log/agent.log",
		"Restart=always",
		"[Install]",
		"WantedBy=multi-user.target",
	}
	for _, check := range checks {
		if !strings.Contains(content, check) {
			t.Errorf("expected config to contain %q, got:\n%s", check, content)
		}
	}
}

func TestDefaultServiceManager_Create_ServiceConfigOmitsUserByDefault(t *testing.T) {
	tmpFile := newTempConfigPath(t)
	mock := &mockSystemCtl{configFilePath: tmpFile}
	sm := &defaultServiceManager{system: mock}

	if _, err := sm.Create(AgentParams{Name: "my-service"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}
	if strings.Contains(string(data), "User=") {
		t.Errorf("expected no User= directive when ServiceUsername unset, got:\n%s", data)
	}
}

func TestDefaultServiceManager_Create_ServiceConfigIncludesUser(t *testing.T) {
	tmpFile := newTempConfigPath(t)
	mock := &mockSystemCtl{configFilePath: tmpFile}
	sm := &defaultServiceManager{system: mock}

	if _, err := sm.Create(AgentParams{Name: "my-service", ServiceUsername: "rewst"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, err := os.ReadFile(tmpFile)
	if err != nil {
		t.Fatalf("failed to read config file: %v", err)
	}
	content := string(data)
	for _, check := range []string{"User=rewst", "Group=rewst"} {
		if !strings.Contains(content, check) {
			t.Errorf("expected config to contain %q, got:\n%s", check, content)
		}
	}
}

func TestDefaultServiceManager_Create_WriteFileError(t *testing.T) {
	mock := &mockSystemCtl{configFilePath: "/nonexistent/dir/file.service"}
	sm := &defaultServiceManager{system: mock}

	_, err := sm.Create(AgentParams{Name: "test-svc"})

	if err == nil {
		t.Error("expected error on WriteFile failure, got nil")
	}
}

func TestDefaultServiceManager_Create_DaemonReloadError(t *testing.T) {
	mock := &mockSystemCtl{
		configFilePath: newTempConfigPath(t),
		runErrOnCmd:    "daemon-reload",
		runErr:         errors.New("daemon-reload failed"),
	}
	sm := &defaultServiceManager{system: mock}

	_, err := sm.Create(AgentParams{Name: "test-svc"})

	if err == nil {
		t.Error("expected error on daemon-reload failure, got nil")
	}
}

func TestDefaultServiceManager_Create_EnableError(t *testing.T) {
	mock := &mockSystemCtl{
		configFilePath: newTempConfigPath(t),
		runErrOnCmd:    "enable",
		runErr:         errors.New("enable failed"),
	}
	sm := &defaultServiceManager{system: mock}

	_, err := sm.Create(AgentParams{Name: "test-svc"})

	if err == nil {
		t.Error("expected error on enable failure, got nil")
	}
}

// defaultServiceManager.Open tests

func TestDefaultServiceManager_Open_Success(t *testing.T) {
	mock := &mockSystemCtl{}
	sm := &defaultServiceManager{system: mock}

	svc, err := sm.Open("test-svc")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if svc == nil {
		t.Fatal("expected service, got nil")
	}
	if len(mock.runCalls) != 1 || mock.runCalls[0][0] != "is-enabled" ||
		mock.runCalls[0][1] != "test-svc" {
		t.Errorf("expected Run(is-enabled, test-svc), got %v", mock.runCalls)
	}
}

func TestDefaultServiceManager_Open_Error(t *testing.T) {
	mock := &mockSystemCtl{runErr: errors.New("status failed")}
	sm := &defaultServiceManager{system: mock}

	_, err := sm.Open("test-svc")

	if err == nil {
		t.Error("expected error, got nil")
	}
}
