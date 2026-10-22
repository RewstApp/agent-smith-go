//go:build e2e

// Package e2e drives the real agent binary, installed as a real service, through
// the scenarios that need nothing but stubs the repository owns: the auto-update
// retry schedule and its digest checks, the startup sweeps, and (on Windows) the
// wedged-service and locked-directory paths. Each scenario used to be eight to
// fifteen workflow steps per platform - start a fixture, capture a baseline,
// install, poll the log, assert through a composite action - and could only be
// run by dispatching the whole e2e workflow. Here it is a Go test: the config
// endpoint and the release endpoint are httptest servers in this process, the
// binary is a subprocess under the test's control, and the assertions read the
// log, the filesystem and the stubs' request records directly (sc-126321).
//
// The package sits behind the `e2e` build tag so it stays out of `go test
// ./...`: it installs a system service and needs elevation. Run it against a
// built binary with
//
//	go test -c -tags e2e -o /tmp/e2e.test ./test/e2e
//	sudo E2E_BINARY=./dist/rewst_agent_config.linux.bin \
//	     E2E_IT_BINARY=./dist/rewst_agent_config.linux.it.bin \
//	     E2E_ORG_ID=<org id> /tmp/e2e.test -test.v -test.run AutoUpdate
//
// (an elevated shell and the .exe names on Windows). The integration-test
// binary is the one scripts/build_integration_test.ps1 produces: it is what
// shortens the update interval and retry schedule to seconds and honours the
// release-url override file the auto-update scenarios depend on. The release
// binary installs and uninstalls, and is what the sweeps and the
// locked-directory scenario run.
//
// Every wait is a bounded poll on the observable the scenario cares about (a
// log-line count, a service state, a file); there are no guessed sleeps
// (sc-119840). Each top-level test installs fresh and uninstalls on cleanup, so
// the tests are independent and run in any order, one at a time. Set E2E_KEEP to
// leave the installation in place after a failure for inspection.
package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
)

const (
	// serviceCommandTimeout bounds every service-manager call the harness
	// makes, for the same reason the agent bounds its own: a wedged launchd or
	// a D-Bus stall must fail the test, not hang the job.
	serviceCommandTimeout = 2 * time.Minute

	// pollInterval is how often a bounded wait re-reads its observable.
	pollInterval = 500 * time.Millisecond

	// releaseURLOverrideFile is the file name the integration build reads the
	// release endpoint from (releaseUrlOverrideFileStr in
	// scripts/build_integration_test.ps1). The agent reads it when the service
	// constructs its updater, so it has to be in place before the service
	// (re)starts.
	releaseURLOverrideFile = "release_url_override"

	// commandTimeout bounds every invocation of the agent binary. The longest
	// legitimate run is a wedged-stop abort: the integration build's 25 s stop
	// deadline plus its 25 s exit wait, plus service-manager latency.
	commandTimeout = 5 * time.Minute
)

// required makes a missing E2E_BINARY or E2E_IT_BINARY a failure rather than a
// skip. CI passes it (the flag survives sudo where the environment might not,
// see sc-129246) so a forwarding regression cannot report a green job that ran
// nothing; a developer's `go test -tags e2e` without the variables still skips.
var required = flag.Bool(
	"e2e.required",
	false,
	"fail instead of skipping when E2E_BINARY or E2E_IT_BINARY is unset",
)

// serviceController is the harness's view of the platform service manager.
// Status reports one of "running", "stopped", "start-pending", "stop-pending"
// or "absent"; BinaryPath and SetBinaryPath exist for the Windows wedged-service
// scenario and return errNotSupported elsewhere.
type serviceController interface {
	Status() (string, error)
	Exists() bool
	Stop() error
	Start() error
	Restart() error
	BinaryPath() (string, error)
	SetBinaryPath(path string) error
	// Kill terminates the service process outright, for a cleanup that has to
	// get past a fixture that will not stop.
	Kill() error
}

// Harness holds one installation of the agent for the duration of a test.
type Harness struct {
	t           *testing.T
	Binary      string
	ITBinary    string
	OrgID       string
	GithubToken string
	Config      *configEndpoint
	svc         serviceController
	uninstalled bool
}

// New reads the environment, checks the process is elevated, starts the
// in-process config endpoint and registers the uninstall cleanup. It skips (not
// fails) when E2E_BINARY or E2E_IT_BINARY is unset so `go vet -tags e2e` and a
// casual `go test -tags e2e ./test/e2e` on a developer machine stay green,
// unless -e2e.required is passed.
func New(t *testing.T) *Harness {
	t.Helper()
	binary := os.Getenv("E2E_BINARY")
	itBinary := os.Getenv("E2E_IT_BINARY")
	for name, value := range map[string]string{"E2E_BINARY": binary, "E2E_IT_BINARY": itBinary} {
		if value != "" {
			continue
		}
		msg := name + " is not set; see the README's \"Go e2e scenarios\" section"
		if *required {
			t.Fatal(msg + " (-e2e.required: the environment did not reach the test binary)")
		}
		t.Skip(msg)
	}
	orgID := os.Getenv("E2E_ORG_ID")
	if orgID == "" {
		t.Fatal("E2E_ORG_ID is required: it names the installation's paths and service")
	}
	requireElevation(t)

	h := &Harness{t: t, OrgID: orgID, GithubToken: os.Getenv("E2E_GITHUB_TOKEN")}
	h.Binary = absExecutable(t, binary)
	h.ITBinary = absExecutable(t, itBinary)
	h.Config = newConfigEndpoint(t, orgID)
	h.svc = newServiceController(agent.GetServiceName(orgID))
	t.Cleanup(h.teardown)
	return h
}

func absExecutable(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("resolving %s: %v", path, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		t.Fatalf("agent binary %s: %v", abs, err)
	}
	if info.IsDir() {
		t.Fatalf("agent binary %s is a directory", abs)
	}
	return abs
}

// Install runs the release binary in config mode against the in-process config
// endpoint with auto-updates off, then waits for the service to be running.
func (h *Harness) Install(extra ...string) string {
	h.t.Helper()
	args := []string{
		"--config-url", h.Config.URL(),
		"--config-secret", h.Config.Secret,
		"--org-id", h.OrgID,
		"--no-auto-updates",
	}
	args = append(args, h.tokenArgs()...)
	args = append(args, extra...)
	out, _, err := h.Run(h.Binary, args...)
	if err != nil {
		h.t.Fatalf("install failed: %v\n%s", err, out)
	}
	h.uninstalled = false
	h.WaitForServiceStatus("running", 2*time.Minute)
	return out
}

// Update reinstalls over the current installation with the given binary
// (`--update`), which stops the service, replaces the executable and starts it
// again. autoUpdates true leaves the updater on, the way an auto-update
// scenario needs it; false passes --no-auto-updates.
func (h *Harness) Update(binary string, autoUpdates bool, extra ...string) string {
	h.t.Helper()
	args := []string{"--update", "--org-id", h.OrgID}
	if !autoUpdates {
		args = append(args, "--no-auto-updates")
	}
	args = append(args, h.tokenArgs()...)
	args = append(args, extra...)
	out, _, err := h.Run(binary, args...)
	if err != nil {
		h.t.Fatalf("update with %s failed: %v\n%s", filepath.Base(binary), err, out)
	}
	h.WaitForServiceStatus("running", 2*time.Minute)
	return out
}

// Uninstall runs the release binary's uninstall and marks the installation gone
// so the cleanup does not try again.
func (h *Harness) Uninstall() string {
	h.t.Helper()
	out, _, err := h.Run(h.Binary, "--uninstall", "--org-id", h.OrgID)
	h.uninstalled = true
	if err != nil {
		h.t.Fatalf("uninstall failed: %v\n%s", err, out)
	}
	return out
}

// MarkUninstalled records that a scenario ran the uninstall itself.
func (h *Harness) MarkUninstalled() { h.uninstalled = true }

func (h *Harness) tokenArgs() []string {
	if h.GithubToken == "" {
		return nil
	}
	return []string{"--github-token", h.GithubToken}
}

// Run invokes an agent binary and returns its combined output, how long it ran
// and its exit error, so a scenario can assert on a run that is expected to
// report failure through its log rather than its exit status.
func (h *Harness) Run(binary string, args ...string) (string, time.Duration, error) {
	h.t.Helper()
	cmd := exec.Command(binary, args...)
	cmd.Dir = filepath.Dir(binary)
	started := time.Now()
	done := make(chan struct{})
	var out []byte
	var err error
	go func() {
		out, err = cmd.CombinedOutput()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(commandTimeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		<-done
		return string(out), time.Since(started), fmt.Errorf(
			"%s %s did not return within %s",
			filepath.Base(binary), strings.Join(args, " "), commandTimeout,
		)
	}
	elapsed := time.Since(started)
	h.t.Logf(
		"%s %s -> %s (%v)",
		filepath.Base(binary), redact(args), exitLabel(err), elapsed.Round(100*time.Millisecond),
	)
	return string(out), elapsed, err
}

func redact(args []string) string {
	out := make([]string, len(args))
	copy(out, args)
	for i := 0; i+1 < len(out); i++ {
		if out[i] == "--github-token" || out[i] == "--config-secret" {
			out[i+1] = "***"
		}
	}
	return strings.Join(out, " ")
}

func exitLabel(err error) string {
	if err == nil {
		return "exit 0"
	}
	return err.Error()
}

// Paths of the installation, from the same functions the agent uses.
func (h *Harness) DataDir() string    { return agent.GetDataDirectory(h.OrgID) }
func (h *Harness) ProgramDir() string { return agent.GetProgramDirectory(h.OrgID) }
func (h *Harness) ScriptsDir() string { return agent.GetScriptsDirectory(h.OrgID) }
func (h *Harness) UpdatesDir() string { return agent.GetUpdatesDirectory(h.OrgID) }
func (h *Harness) ConfigPath() string { return agent.GetConfigFilePath(h.OrgID) }
func (h *Harness) ExePath() string    { return agent.GetAgentExecutablePath(h.OrgID) }
func (h *Harness) LogPath() string    { return agent.GetLogFilePath(h.OrgID) }

// SetReleaseURL points the installed integration build's auto-updater at url by
// writing the override file in the data directory. It takes effect when the
// service next starts.
func (h *Harness) SetReleaseURL(url string) {
	h.t.Helper()
	path := filepath.Join(h.DataDir(), releaseURLOverrideFile)
	if err := os.WriteFile(path, []byte(url), 0o644); err != nil {
		h.t.Fatalf("writing release url override %s: %v", path, err)
	}
	h.t.Logf("release url override -> %s", url)
}

// ClearReleaseURL removes the override so the updater is back on the real
// endpoint at the next service start.
func (h *Harness) ClearReleaseURL() {
	h.t.Helper()
	path := filepath.Join(h.DataDir(), releaseURLOverrideFile)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		h.t.Fatalf("removing release url override %s: %v", path, err)
	}
}

// readLog returns the log file's contents, or "" when it does not exist yet.
func (h *Harness) readLog() string {
	b, err := os.ReadFile(h.LogPath())
	if err != nil {
		return ""
	}
	return string(b)
}

// LogCount is how many log lines contain needle.
func (h *Harness) LogCount(needle string) int {
	return strings.Count(h.readLog(), needle)
}

// LogLines returns every log line containing needle, in order.
func (h *Harness) LogLines(needle string) []string {
	var out []string
	for _, ln := range strings.Split(h.readLog(), "\n") {
		if strings.Contains(ln, needle) {
			out = append(out, strings.TrimRight(ln, "\r"))
		}
	}
	return out
}

// LogTail returns the last n lines of the log.
func (h *Harness) LogTail(n int) string {
	lines := strings.Split(strings.TrimRight(h.readLog(), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// WaitForLogCount waits until more than above lines contain needle and returns
// the count, failing the test with the log tail when the deadline passes.
func (h *Harness) WaitForLogCount(needle string, above int, timeout time.Duration) int {
	h.t.Helper()
	var count int
	ok := waitFor(timeout, func() bool {
		count = h.LogCount(needle)
		return count > above
	})
	if !ok {
		h.t.Fatalf(
			"%q did not appear more than %d time(s) within %s (count %d)\n---- %s (tail) ----\n%s",
			needle, above, timeout, count, h.LogPath(), h.LogTail(40),
		)
	}
	h.t.Logf("%q count %d (> %d)", needle, count, above)
	return count
}

// Service control.

func (h *Harness) ServiceStatus() string {
	h.t.Helper()
	s, err := h.svc.Status()
	if err != nil {
		h.t.Fatalf("querying service: %v", err)
	}
	return s
}

func (h *Harness) ServiceExists() bool { return h.svc.Exists() }

// StopService asks the service manager to stop the agent and returns at once;
// the caller measures what happens next.
func (h *Harness) StopService() {
	h.t.Helper()
	if err := h.svc.Stop(); err != nil {
		h.t.Fatalf("stopping service: %v", err)
	}
}

func (h *Harness) StartService() {
	h.t.Helper()
	if err := h.svc.Start(); err != nil {
		h.t.Fatalf("starting service: %v", err)
	}
	h.WaitForServiceStatus("running", 2*time.Minute)
}

// RestartService stops and starts the agent through the service manager, so
// the startup code (the sweeps, the updater's override read) runs again.
func (h *Harness) RestartService() {
	h.t.Helper()
	if err := h.svc.Restart(); err != nil {
		h.t.Fatalf("restarting service: %v", err)
	}
	h.WaitForServiceStatus("running", 2*time.Minute)
}

// WaitForServiceStatus polls the service manager until the service reports want.
func (h *Harness) WaitForServiceStatus(want string, timeout time.Duration) {
	h.t.Helper()
	var last string
	ok := waitFor(timeout, func() bool {
		s, err := h.svc.Status()
		if err != nil {
			last = "error: " + err.Error()
			return false
		}
		last = s
		return s == want
	})
	if !ok {
		h.t.Fatalf(
			"service did not reach %q within %s (last %q)\n---- log tail ----\n%s",
			want, timeout, last, h.LogTail(40),
		)
	}
}

// waitFor polls cond every pollInterval until it returns true or timeout
// passes. It is the one place the package sleeps.
func waitFor(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(pollInterval) // sleep-ok: poll interval inside a bounded wait
	}
}

// sha256File returns the hex digest of a file's contents.
func sha256File(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("opening %s for hashing: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		t.Fatalf("hashing %s: %v", path, err)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func (h *Harness) teardown() {
	t := h.t
	if t.Failed() {
		t.Logf("---- %s (last 80 lines) ----\n%s", h.LogPath(), h.LogTail(80))
	}
	if os.Getenv("E2E_KEEP") != "" {
		t.Logf("E2E_KEEP set; leaving the installation in place")
		return
	}
	if !h.uninstalled {
		out, _, err := h.Run(h.Binary, "--uninstall", "--org-id", h.OrgID)
		if err != nil {
			t.Logf("cleanup uninstall failed: %v\n%s", err, out)
		}
		h.uninstalled = true
	}
	for _, dir := range []string{h.DataDir(), h.ProgramDir(), h.ScriptsDir()} {
		if err := os.RemoveAll(dir); err != nil {
			t.Logf("cleanup: removing %s: %v", dir, err)
		}
	}
}
