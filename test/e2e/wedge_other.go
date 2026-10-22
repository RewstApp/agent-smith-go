//go:build e2e && !windows

package e2e

import "testing"

// wedgedService has no counterpart off Windows. TestWedgedService skips before
// reaching it; these report an error rather than stopping the test so the
// shared test body is not dead code to the analysers on this platform.
type wedgedService struct{}

func wedgeService(t *testing.T, _ *Harness) *wedgedService {
	t.Helper()
	t.Error("a wedged StopPending service exists only on Windows")
	return &wedgedService{}
}

func (w *wedgedService) rewedge(t *testing.T) {
	t.Helper()
	t.Error("not supported on this platform")
}

func (w *wedgedService) restore(t *testing.T) {
	t.Helper()
	t.Error("not supported on this platform")
}
