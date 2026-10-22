//go:build e2e

package e2e

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestWedgedService puts the agent's Windows service into the state sc-106108
// fixed - a stop control accepted, StopPending reported, Stopped never reached
// - by pointing the service at the test/wedgedservice fixture, and asserts
// that an update and an uninstall both abort within their bounded stop wait
// and leave the installation byte-identical, then that an ordinary update
// succeeds once the service is restored. Windows only: Linux and macOS have
// no equivalent state.
func TestWedgedService(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("a wedged StopPending service exists only on Windows")
	}
	h := New(t)
	h.Install()

	exeHash := sha256File(t, h.ExePath())
	cfgHash := sha256File(t, h.ConfigPath())
	w := wedgeService(t, h)

	t.Run("UpdateAbortsOnWedgedStop", func(t *testing.T) {
		// The integration build is the invoker: its stop deadline is 25 s
		// (scripts/build_integration_test.ps1) against the release default
		// of five minutes, which unit tests cover.
		args := append([]string{"--update", "--org-id", h.OrgID}, h.tokenArgs()...)
		out, elapsed, _ := h.Run(h.ITBinary, args...)
		t.Logf("---- updater output after %s ----\n%s", elapsed.Round(100*time.Millisecond), out)

		// Returning at all is the headline: before the fix this call never
		// came back. The floor proves it waited out its deadline rather than
		// failing fast for another reason.
		if elapsed < 20*time.Second {
			t.Errorf(
				"update returned after %s, before its 25 s stop deadline could elapse",
				elapsed.Round(100*time.Millisecond),
			)
		}
		if elapsed > 3*time.Minute {
			t.Errorf(
				"update took %s, far past its 25 s stop deadline",
				elapsed.Round(100*time.Millisecond),
			)
		}
		assertContainsAll(
			t,
			out,
			"Failed to stop service",
			"did not stop within",
			"last observed state StopPending",
			"Update aborted; existing installation left untouched",
		)
		assertContainsNone(
			t,
			out,
			"Configuration successfully updated",
			"Agent installed to",
			"Service started",
		)
		assertUnchanged(t, h, exeHash, cfgHash)
	})

	w.rewedge(t)

	t.Run("UninstallAbortsOnWedgedStop", func(t *testing.T) {
		out, elapsed, _ := h.Run(h.ITBinary, "--uninstall", "--org-id", h.OrgID)
		t.Logf(
			"---- uninstaller output after %s ----\n%s",
			elapsed.Round(100*time.Millisecond),
			out,
		)
		if elapsed < 20*time.Second {
			t.Errorf(
				"uninstall returned after %s, before its 25 s stop deadline could elapse",
				elapsed.Round(100*time.Millisecond),
			)
		}
		if elapsed > 3*time.Minute {
			t.Errorf(
				"uninstall took %s, far past its 25 s stop deadline",
				elapsed.Round(100*time.Millisecond),
			)
		}
		assertContainsAll(
			t,
			out,
			"Failed to stop service",
			"did not stop within",
			"Uninstall aborted; nothing was removed",
		)
		assertContainsNone(t, out, "Service deleted", "Directory deleted")
		// Fully intact or fully removed, never mixed.
		if !h.ServiceExists() {
			t.Error("the service registration was deleted")
		}
		for _, dir := range []string{h.DataDir(), h.ProgramDir()} {
			if _, err := os.Stat(dir); err != nil {
				t.Errorf("installed directory was removed: %s (%v)", dir, err)
			}
		}
		assertUnchanged(t, h, exeHash, cfgHash)
	})

	w.restore(t)

	t.Run("SubsequentUpdateSucceedsAfterRecovery", func(t *testing.T) {
		// The bound must only catch a wedge: once the service is the real
		// agent again, an ordinary update stops it, replaces the binary and
		// starts it with no behavioural change.
		out := h.Update(h.Binary, false)
		assertContainsAll(t, out, "Agent installed to", "Service started")
		assertContainsNone(t, out, "Failed to stop service")
	})
}

func assertUnchanged(t *testing.T, h *Harness, exeHash, cfgHash string) {
	t.Helper()
	if got := sha256File(t, h.ExePath()); got != exeHash {
		t.Errorf("agent executable changed: %s -> %s", exeHash, got)
	}
	if got := sha256File(t, h.ConfigPath()); got != cfgHash {
		t.Errorf("config file changed: %s -> %s", cfgHash, got)
	}
}

func assertContainsAll(t *testing.T, out string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if !strings.Contains(out, n) {
			t.Errorf("missing from the output: %q", n)
		}
	}
}

func assertContainsNone(t *testing.T, out string, needles ...string) {
	t.Helper()
	for _, n := range needles {
		if strings.Contains(out, n) {
			t.Errorf("the output should not contain %q", n)
		}
	}
}
