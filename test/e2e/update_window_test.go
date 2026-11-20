//go:build e2e

package e2e

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/go-hclog"
)

// logStamp is the hclog timestamp that opens every agent log line.
var logStamp = regexp.MustCompile(`^(\S+) `)

// TestUpdateWindow drives the integration build against a release stub that
// publishes a newer full release, with an update window that opens a few
// minutes after the agent restarts (sc-132993): the first check, 30 s after
// the restart, finds the release and waits for the window instead of
// updating; once the window opens it checks again and decides to update, and
// the window is saved into the config file. The window is minute-granular,
// so it opens at the third minute boundary from now - two to three minutes
// away - which leaves the restart and the first check room to land before it.
func TestUpdateWindow(t *testing.T) {
	h := New(t)
	h.Install()

	stub := newReleaseStub(t, "v99.0.0", "", "")
	stub.SetOK(true)
	h.SetReleaseURL(stub.URL())

	opens := time.Now().Truncate(time.Minute).Add(3 * time.Minute)
	window := fmt.Sprintf(
		"%s-%s", opens.Format("15:04"), opens.Add(30*time.Minute).Format("15:04"),
	)
	waiting := h.LogCount("Update available; waiting for the update window")
	updating := h.LogCount("Updating agent")
	h.Update(h.ITBinary, true, "--update-window", window)

	cfg := readTextFile(t, h.ConfigPath())
	if !strings.Contains(cfg, `"update_window": "`+window+`"`) {
		t.Errorf("the config file does not carry the window %s:\n%s", window, cfg)
	}

	h.WaitForLogCount(
		"Update available; waiting for the update window", waiting, updateInterval+time.Minute,
	)
	if !time.Now().Before(opens) {
		t.Fatalf("the first check came after the window opened at %s; the scenario proves nothing",
			opens.Format(time.RFC3339))
	}
	line := h.LogLines("Update available; waiting for the update window")[waiting]
	for _, want := range []string{"version=v99.0.0", "window=" + window} {
		if !strings.Contains(line, want) {
			t.Errorf("the waiting line lacks %q: %s", want, line)
		}
	}

	// Hold until just before the window opens: nothing may have decided to
	// update in the meantime.
	if d := time.Until(opens) - 5*time.Second; d > 0 {
		time.Sleep(d) // sleep-ok: the closed window is the thing under test
	}
	if got := h.LogCount("Updating agent"); got != updating {
		t.Fatalf("the agent decided to update %d time(s) before the window opened at %s",
			got-updating, opens.Format(time.RFC3339))
	}

	h.WaitForLogCount("Updating agent", updating, time.Until(opens)+2*time.Minute)
	decided := h.LogLines("Updating agent")[updating]
	m := logStamp.FindStringSubmatch(decided)
	if m == nil {
		t.Fatalf("no timestamp on: %s", decided)
	}
	stamp, err := time.Parse(hclog.TimeFormat, m[1])
	if err != nil {
		t.Fatalf("could not parse the timestamp of %s: %v", decided, err)
	}
	if stamp.Before(opens) {
		t.Errorf("decided to update at %s, before the window opened at %s",
			stamp.Format(time.RFC3339), opens.Format(time.RFC3339))
	}
	if h.LogCount("Update window open; checking for the release again") == 0 {
		t.Error("the wait did not log the window opening")
	}

	h.ClearReleaseURL()
	h.Update(h.Binary, false, "--update-window", "")
}
