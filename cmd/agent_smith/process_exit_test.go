package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/utils"
)

// fakeClock drives the bounded waits without ever sleeping, so a test can burn a
// two minute deadline instantly and still assert exactly how much waiting the
// code asked for.
type fakeClock struct {
	current time.Time
	slept   time.Duration
	sleeps  int
}

func newFakeClock() *fakeClock {
	return &fakeClock{current: time.Unix(0, 0)}
}

func (c *fakeClock) Now() time.Time { return c.current }

func (c *fakeClock) Sleep(d time.Duration) {
	c.slept += d
	c.sleeps++
	c.current = c.current.Add(d)
}

func (c *fakeClock) options(timeout time.Duration, poll time.Duration) exitWaitOptions {
	return exitWaitOptions{
		timeout:           timeout,
		deregisterTimeout: timeout,
		pollInterval:      poll,
		now:               c.Now,
		sleep:             c.Sleep,
		processRunning:    noProcessRunning,
	}
}

// noProcessRunning stands in for the process-table scan in tests that are about
// the other signals, so they neither sweep the real process table nor depend on
// what happens to be running on the machine.
func noProcessRunning(string) (bool, error) { return false, nil }

// stubExitWait keeps the documented defaults for everything except the process
// scan, which unit tests must not perform against the real machine.
func stubExitWait() exitWaitOptions {
	return exitWaitOptions{processRunning: noProcessRunning}
}

// ── waitForAgentProcessExit ───────────────────────────────────────────────────

// A process that is already gone must cost nothing: the whole point of replacing
// the fixed five second sleep is that a healthy update gets faster, not slower.
func TestWaitForAgentProcessExit_ExitsImmediately(t *testing.T) {
	clock := newFakeClock()
	probes := 0
	fsys := &mockFileSystem{
		executableInUseFunc: func(string) (bool, error) {
			probes++
			return false, nil
		},
	}

	err := waitForAgentProcessExit(
		utils.ConfigureLogger("test", os.Stdout, utils.Default),
		&mockService{isActive: false},
		fsys,
		"/opt/rewst/agent",
		clock.options(2*time.Minute, 250*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("expected the wait to succeed, got %v", err)
	}
	if clock.sleeps != 0 {
		t.Errorf(
			"expected no waiting for a process that already exited, slept %d times",
			clock.sleeps,
		)
	}
	if probes != 1 {
		t.Errorf("expected a single executable probe, got %d", probes)
	}
}

// A process that takes a few polls to release the executable must be waited out
// and then proceed — this is the slow-drain endpoint the fixed sleep raced.
func TestWaitForAgentProcessExit_ExitsPartwayThrough(t *testing.T) {
	clock := newFakeClock()
	activeChecks := 0
	probes := 0
	svc := &mockService{isActive: true}
	fsys := &mockFileSystem{
		executableInUseFunc: func(string) (bool, error) {
			probes++
			// The image stays open for two polls after the service reports stopped.
			return probes <= 4, nil
		},
	}

	// The service reports active for the first two observations, then stopped.
	svcActive := func() bool {
		activeChecks++
		return activeChecks <= 2
	}
	svc.isActiveFunc = svcActive

	err := waitForAgentProcessExit(
		utils.ConfigureLogger("test", os.Stdout, utils.Default),
		svc,
		fsys,
		"/opt/rewst/agent",
		clock.options(2*time.Minute, 250*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("expected the wait to succeed once the process exited, got %v", err)
	}
	if clock.sleeps != 4 {
		t.Errorf("expected 4 polls before both signals cleared, got %d", clock.sleeps)
	}
	if clock.slept >= 2*time.Minute {
		t.Errorf("expected the wait to return well inside the deadline, waited %s", clock.slept)
	}
}

// A process that never exits must be given up on at the deadline with an error
// naming what was still outstanding, so the caller can abort before writing.
func TestWaitForAgentProcessExit_NeverExits(t *testing.T) {
	clock := newFakeClock()
	fsys := &mockFileSystem{
		executableInUseFunc: func(string) (bool, error) { return true, nil },
	}

	err := waitForAgentProcessExit(
		utils.ConfigureLogger("test", os.Stdout, utils.Default),
		&mockService{isActive: true},
		fsys,
		"/opt/rewst/agent",
		clock.options(2*time.Minute, 250*time.Millisecond),
	)
	if err == nil {
		t.Fatal("expected an error when the process never exits")
	}
	if !strings.Contains(err.Error(), "2m0s") {
		t.Errorf("expected the deadline in the error, got %q", err)
	}
	if !strings.Contains(err.Error(), "/opt/rewst/agent") {
		t.Errorf("expected the executable path in the error, got %q", err)
	}
	if !strings.Contains(err.Error(), "still reports the service active") {
		t.Errorf("expected the service state in the error, got %q", err)
	}
	if clock.slept < 2*time.Minute {
		t.Errorf("expected the full deadline to be waited out, waited %s", clock.slept)
	}
	if clock.slept > 2*time.Minute+250*time.Millisecond {
		t.Errorf("expected the wait to stay bounded by the deadline, waited %s", clock.slept)
	}
}

// The wait must not conclude "still running" from a probe that could not run at
// all: an unreadable path is not evidence of a live process, and the atomic
// write covers the residual risk.
func TestWaitForAgentProcessExit_ProbeErrorFallsBackToServiceState(t *testing.T) {
	clock := newFakeClock()
	fsys := &mockFileSystem{
		executableInUseFunc: func(string) (bool, error) {
			return false, errors.New("permission denied")
		},
	}

	err := waitForAgentProcessExit(
		utils.ConfigureLogger("test", os.Stdout, utils.Default),
		&mockService{isActive: false},
		fsys,
		"/opt/rewst/agent",
		clock.options(time.Minute, 250*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("expected the wait to fall back to the service state, got %v", err)
	}
	if clock.sleeps != 0 {
		t.Errorf(
			"expected no waiting when the service is already stopped, slept %d times",
			clock.sleeps,
		)
	}
}

// A process that is still executing the agent binary keeps the wait open even
// when the service manager and the file lock both say otherwise — the case that
// matters on macOS, where a running image can be opened for writing.
func TestWaitForAgentProcessExit_WaitsForRunningProcess(t *testing.T) {
	clock := newFakeClock()
	scans := 0
	opts := clock.options(2*time.Minute, 250*time.Millisecond)
	opts.processRunning = func(string) (bool, error) {
		scans++
		return scans <= 3, nil
	}

	err := waitForAgentProcessExit(
		utils.ConfigureLogger("test", os.Stdout, utils.Default),
		&mockService{isActive: false},
		&mockFileSystem{},
		"/opt/rewst/agent",
		opts,
	)
	if err != nil {
		t.Fatalf("expected the wait to succeed once the process was gone, got %v", err)
	}
	if clock.sleeps != 3 {
		t.Errorf("expected 3 polls while the process was still running, got %d", clock.sleeps)
	}
}

func TestWaitForAgentProcessExit_ProcessOutlivesDeadline(t *testing.T) {
	clock := newFakeClock()
	opts := clock.options(2*time.Minute, 250*time.Millisecond)
	opts.processRunning = func(string) (bool, error) { return true, nil }

	err := waitForAgentProcessExit(
		utils.ConfigureLogger("test", os.Stdout, utils.Default),
		&mockService{isActive: false},
		&mockFileSystem{},
		"/opt/rewst/agent",
		opts,
	)
	if err == nil {
		t.Fatal("expected an error when a process keeps running the agent binary")
	}
	if !strings.Contains(err.Error(), "a process is still running /opt/rewst/agent") {
		t.Errorf("expected the live process named in the error, got %q", err)
	}
}

// A process scan that cannot run is not evidence either way and must not pin the
// wait open until the deadline.
func TestWaitForAgentProcessExit_ProcessScanErrorFallsBack(t *testing.T) {
	clock := newFakeClock()
	opts := clock.options(time.Minute, 250*time.Millisecond)
	opts.processRunning = func(string) (bool, error) {
		return false, errors.New("cannot enumerate processes")
	}

	err := waitForAgentProcessExit(
		utils.ConfigureLogger("test", os.Stdout, utils.Default),
		&mockService{isActive: false},
		&mockFileSystem{},
		"/opt/rewst/agent",
		opts,
	)
	if err != nil {
		t.Fatalf("expected the wait to fall back to the remaining signals, got %v", err)
	}
	if clock.sleeps != 0 {
		t.Errorf("expected no waiting, slept %d times", clock.sleeps)
	}
}

// A missing service handle (the registration is already gone) leaves the
// executable probe as the only signal, which must still be honoured.
func TestWaitForAgentProcessExit_NilServiceUsesExecutableSignal(t *testing.T) {
	clock := newFakeClock()
	probes := 0
	fsys := &mockFileSystem{
		executableInUseFunc: func(string) (bool, error) {
			probes++
			return probes <= 2, nil
		},
	}

	err := waitForAgentProcessExit(
		utils.ConfigureLogger("test", os.Stdout, utils.Default),
		nil,
		fsys,
		"/opt/rewst/agent",
		clock.options(time.Minute, 250*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("expected the wait to succeed, got %v", err)
	}
	if clock.sleeps != 2 {
		t.Errorf("expected 2 polls while the executable was held open, got %d", clock.sleeps)
	}
}

func TestWaitForAgentProcessExit_DefaultsAreDocumentedValues(t *testing.T) {
	opts := exitWaitOptions{}.resolved()
	if opts.timeout != agentProcessExitTimeout {
		t.Errorf("expected the default timeout %s, got %s", agentProcessExitTimeout, opts.timeout)
	}
	if opts.timeout <= 5*time.Second {
		t.Errorf(
			"expected a deadline substantially longer than the old 5s sleep, got %s",
			opts.timeout,
		)
	}
	if opts.pollInterval != agentProcessExitPollInterval {
		t.Errorf(
			"expected the default poll interval %s, got %s",
			agentProcessExitPollInterval,
			opts.pollInterval,
		)
	}
}

// ── waitForServiceDeregistration ──────────────────────────────────────────────

func TestWaitForServiceDeregistration_AlreadyGone(t *testing.T) {
	clock := newFakeClock()
	mgr := &mockServiceManager{openErr: errors.New("service does not exist")}

	if err := waitForServiceDeregistration(
		mgr,
		"svc",
		clock.options(time.Minute, time.Second),
	); err != nil {
		t.Fatalf("expected success when the registration is already gone, got %v", err)
	}
	if clock.sleeps != 0 {
		t.Errorf("expected no waiting, slept %d times", clock.sleeps)
	}
}

func TestWaitForServiceDeregistration_NeverDisappears(t *testing.T) {
	clock := newFakeClock()
	mgr := &mockServiceManager{openService: &mockService{}}

	err := waitForServiceDeregistration(mgr, "svc", clock.options(30*time.Second, time.Second))
	if err == nil {
		t.Fatal("expected an error when the registration outlives the deadline")
	}
	if !strings.Contains(err.Error(), "svc") {
		t.Errorf("expected the service name in the error, got %q", err)
	}
	if clock.slept < 30*time.Second {
		t.Errorf("expected the full deadline to be waited out, waited %s", clock.slept)
	}
}

// ── writeFileAtomic ───────────────────────────────────────────────────────────

func TestWriteFileAtomic_ReplacesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent")
	if err := os.WriteFile(path, []byte("old binary"), utils.DefaultExecutableFileMod); err != nil {
		t.Fatalf("failed to seed the file: %v", err)
	}

	err := writeFileAtomic(
		utils.NewFileSystem(),
		path,
		[]byte("new binary"),
		utils.DefaultExecutableFileMod,
	)
	if err != nil {
		t.Fatalf("expected the write to succeed, got %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read the file back: %v", err)
	}
	if string(got) != "new binary" {
		t.Errorf("expected the new contents, got %q", got)
	}
	if _, err := os.Stat(path + ".new"); !os.IsNotExist(err) {
		t.Errorf("expected the temporary file to be gone, stat returned %v", err)
	}
}

// failingRenameFS commits nothing: it models the destination being unwritable at
// the moment of the rename, which is exactly the sharing violation Windows
// raises when the old process is still holding the image.
type failingRenameFS struct {
	utils.FileSystem
}

func (f *failingRenameFS) Rename(string, string) error {
	return errors.New("sharing violation")
}

// A failed commit must leave the installed binary byte-identical rather than
// truncated: the endpoint keeps running the old agent instead of nothing at all.
func TestWriteFileAtomic_FailedCommitLeavesOriginalIntact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent")
	original := []byte("old binary contents")
	if err := os.WriteFile(path, original, utils.DefaultExecutableFileMod); err != nil {
		t.Fatalf("failed to seed the file: %v", err)
	}

	fsys := &failingRenameFS{FileSystem: utils.NewFileSystem()}
	err := writeFileAtomic(fsys, path, []byte("new"), utils.DefaultExecutableFileMod)
	if err == nil {
		t.Fatal("expected the write to fail")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read the file back: %v", err)
	}
	if string(got) != string(original) {
		t.Errorf("expected the original contents preserved, got %q", got)
	}
	if _, err := os.Stat(path + ".new"); !os.IsNotExist(err) {
		t.Errorf("expected the temporary file to be cleaned up, stat returned %v", err)
	}
}

func TestWriteFileAtomic_WriteFailureLeavesOriginalIntact(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent")
	original := []byte("old binary contents")
	if err := os.WriteFile(path, original, utils.DefaultExecutableFileMod); err != nil {
		t.Fatalf("failed to seed the file: %v", err)
	}

	fsys := &mockFileSystem{
		writeFileFunc: func(name string, _ []byte, _ os.FileMode) error {
			if !strings.HasSuffix(name, ".new") {
				t.Errorf("expected the write to target a temporary file, got %q", name)
			}
			return errors.New("no space left on device")
		},
	}

	if err := writeFileAtomic(
		fsys,
		path,
		[]byte("new"),
		utils.DefaultExecutableFileMod,
	); err == nil {
		t.Fatal("expected the write to fail")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read the file back: %v", err)
	}
	if string(got) != string(original) {
		t.Errorf("expected the original contents preserved, got %q", got)
	}
}

func TestResolveExitTimeout(t *testing.T) {
	original := exitTimeoutOverrideStr
	defer func() { exitTimeoutOverrideStr = original }()

	// Production builds leave the override empty and get the documented deadline.
	exitTimeoutOverrideStr = ""
	if got := resolveExitTimeout(); got != agentProcessExitTimeout {
		t.Errorf("expected the documented deadline %s, got %s", agentProcessExitTimeout, got)
	}

	exitTimeoutOverrideStr = "25s"
	if got := resolveExitTimeout(); got != 25*time.Second {
		t.Errorf("expected the injected deadline 25s, got %s", got)
	}

	// An unparseable or non-positive override must not disable the bound.
	for _, invalid := range []string{"not-a-duration", "0s", "-5s"} {
		exitTimeoutOverrideStr = invalid
		if got := resolveExitTimeout(); got != agentProcessExitTimeout {
			t.Errorf("expected %q to fall back to the documented deadline, got %s", invalid, got)
		}
	}
}
