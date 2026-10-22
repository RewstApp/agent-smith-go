//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestInstall covers the install-time refusals that need a config endpoint
// answering wrongly on purpose.
func TestInstall(t *testing.T) {
	// A 200 whose configuration carries no device_id is what the Rewst config
	// endpoint answers when its integration token has expired or the IoT Hub
	// daily quota is exhausted; it failed every live install for weeks before
	// it was recognised (sc-126322). The install must refuse it at once with
	// the documented message - not retry it as transient - and leave no
	// service registered and no files behind.
	t.Run("FailsFastWhenTheConfigLacksADeviceID", func(t *testing.T) {
		h := New(t)
		h.Config.SetOmitDeviceID(true)
		args := []string{
			"--config-url", h.Config.URL(),
			"--config-secret", h.Config.Secret,
			"--org-id", h.OrgID,
			"--no-auto-updates",
		}
		out, elapsed, err := h.Run(h.Binary, args...)
		h.MarkUninstalled()
		t.Logf("---- installer output after %s ----\n%s", elapsed.Round(100*time.Millisecond), out)
		if err == nil {
			t.Error("the install reported success against a configuration with no device_id")
		}
		if !strings.Contains(out, "missing required field: device_id") {
			t.Error("the output does not name the missing field")
		}
		if n := h.Config.Requests(); n != 1 {
			t.Errorf(
				"the config endpoint was asked %d times; a refusal must not be retried as transient",
				n,
			)
		}
		if elapsed > 30*time.Second {
			t.Errorf(
				"the refusal took %s; it should not have waited on a retry backoff",
				elapsed.Round(time.Second),
			)
		}
		if h.ServiceExists() {
			t.Error("a service was registered despite the refused configuration")
		}
		for _, dir := range []string{h.ProgramDir(), h.ConfigPath()} {
			if _, statErr := os.Stat(dir); statErr == nil {
				t.Errorf("the refused install left %s behind", dir)
			}
		}
	})
}
