//go:build e2e && windows

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// wedgedService is one wedge of the agent's service: its binary path swapped
// for the test/wedgedservice fixture, which accepts a stop control, reports
// StopPending, and reaches Stopped only once its release file exists.
type wedgedService struct {
	h           *Harness
	fixture     string
	logFile     string
	releaseFile string
	original    string
	restored    bool
}

// wedgeService stops the agent, points its service at the fixture and starts
// it, registering a restore for cleanup. The fixture binary comes from
// E2E_WEDGED_SERVICE_BINARY or is built from the checkout.
func wedgeService(t *testing.T, h *Harness) *wedgedService {
	t.Helper()
	work := t.TempDir()
	if strings.ContainsAny(work, " \t") {
		t.Fatalf(
			"working directory %q contains whitespace; the fixture binary path cannot be built safely",
			work,
		)
	}
	w := &wedgedService{
		h:           h,
		fixture:     wedgedServiceBinary(t, work),
		logFile:     filepath.Join(work, "wedged.log"),
		releaseFile: filepath.Join(work, "release"),
	}
	original, err := h.svc.BinaryPath()
	if err != nil {
		t.Fatalf("reading the service's binary path: %v", err)
	}
	w.original = original
	t.Logf("original binary path: %s", original)
	t.Cleanup(func() { w.restore(t) })

	if s := h.ServiceStatus(); s != "stopped" {
		h.StopService()
		h.WaitForServiceStatus("stopped", time.Minute)
	}
	binPath := fmt.Sprintf(
		"%s -name %s -log %s -release %s",
		w.fixture, serviceName(h), w.logFile, w.releaseFile,
	)
	if err := h.svc.SetBinaryPath(binPath); err != nil {
		t.Fatalf("pointing the service at the fixture: %v", err)
	}
	h.StartService()
	t.Logf("%s is running the wedged fixture and will not honour a stop", serviceName(h))
	return w
}

func serviceName(h *Harness) string {
	return fmt.Sprintf("RewstRemoteAgent_%s", h.OrgID)
}

func wedgedServiceBinary(t *testing.T, work string) string {
	t.Helper()
	if p := os.Getenv("E2E_WEDGED_SERVICE_BINARY"); p != "" {
		return absExecutable(t, p)
	}
	out := filepath.Join(work, "wedgedservice.exe")
	cmd := exec.Command("go", "build", "-tags", "integration", "-o", out, "./test/wedgedservice")
	cmd.Dir = repoRoot(t)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the wedged service fixture: %v\n%s", err, b)
	}
	return out
}

// release lets the current wedged run reach Stopped.
func (w *wedgedService) release(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(w.releaseFile, []byte("release"), 0o644); err != nil {
		t.Fatalf("writing the release file: %v", err)
	}
	if !waitFor(time.Minute, func() bool { return w.h.ServiceStatus() == "stopped" }) {
		t.Logf("---- fixture log ----\n%s", readFile(w.logFile))
		t.Fatal("the wedged fixture did not stop after its release file appeared")
	}
	_ = os.Remove(w.releaseFile)
}

// rewedge releases the current run and starts a fresh one. Once a stop has
// been requested the service sits in StopPending, and the agent's IsActive()
// reports only Running as active, so a second scenario needs a fresh wedge.
func (w *wedgedService) rewedge(t *testing.T) {
	t.Helper()
	w.release(t)
	w.h.StartService()
	t.Logf("%s is wedged again and Running", serviceName(w.h))
}

// restore puts the real binary path back and starts the agent. Idempotent.
func (w *wedgedService) restore(t *testing.T) {
	t.Helper()
	if w.restored {
		return
	}
	w.restored = true
	t.Logf("---- fixture log ----\n%s", readFile(w.logFile))
	_ = os.WriteFile(w.releaseFile, []byte("release"), 0o644)
	if !waitFor(time.Minute, func() bool { s, _ := w.h.svc.Status(); return s == "stopped" }) {
		t.Logf("fixture still not stopped; killing it")
		_ = w.h.svc.Kill()
		waitFor(30*time.Second, func() bool { s, _ := w.h.svc.Status(); return s == "stopped" })
	}
	_ = os.Remove(w.releaseFile)
	if err := w.h.svc.SetBinaryPath(w.original); err != nil {
		t.Fatalf("restoring the service's binary path: %v", err)
	}
	if got, _ := w.h.svc.BinaryPath(); got != w.original {
		t.Fatalf("binary path restore did not take; it is now %q", got)
	}
	w.h.StartService()
	t.Logf("%s restored to the real agent and Running", serviceName(w.h))
}

func readFile(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "(" + err.Error() + ")"
	}
	return string(b)
}

// repoRoot finds the module root from the working directory, whether the test
// runs under `go test` (cwd is the package directory) or as a compiled test
// binary launched from the checkout.
func repoRoot(t *testing.T) string {
	t.Helper()
	if root := os.Getenv("E2E_REPO_ROOT"); root != "" {
		return root
	}
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal(
				"could not find the repository root (no go.mod above the working directory); set E2E_REPO_ROOT",
			)
		}
		dir = parent
	}
}
