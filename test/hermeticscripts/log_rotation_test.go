package hermeticscripts

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hermetic-assert-log-rotation checks the agent's rotated log copies are all
// compressed, valid, the agent's own lines, and inside the retention bound
// (sc-132996). The files are laid out here; the agent plays no part.
func TestAssertLogRotation_ChecksTheCompressedCopies(t *testing.T) {
	const line = "2026-11-27T12:00:00Z [INFO]  agent_smith: Subscribed to messages\n"
	gz := func(text string) []byte {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write([]byte(text))
		_ = zw.Close()
		return buf.Bytes()
	}
	layout := func(files map[string][]byte) string {
		dir := t.TempDir()
		log := filepath.Join(dir, "rewst_agent.log")
		files["rewst_agent.log"] = []byte(line)
		files["config.json"] = []byte("{}")
		for name, data := range files {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return log
	}
	assert := func(log string, env ...string) (int, string) {
		return run(t, "hermetic-assert-log-rotation.ps1",
			append([]string{"LOG_FILE=" + log, "MAX_FILES=2", "TIMEOUT_SECONDS=1"}, env...)...)
	}

	good := layout(map[string][]byte{
		"rewst_agent.log.1.gz": gz(line), "rewst_agent.log.2.gz": gz(line),
	})
	if code, out := assert(good, "MIN_COMPRESSED=2"); code != 0 ||
		!strings.Contains(out, "every one compressed") {
		t.Fatalf("two compressed copies failed: exit %d\n%s", code, out)
	}

	for _, tc := range []struct {
		name  string
		files map[string][]byte
		env   string
		says  string
	}{
		{
			"too few",
			map[string][]byte{"rewst_agent.log.1.gz": gz(line)},
			"MIN_COMPRESSED=2", "expected at least 2",
		},
		{"a plain copy left", map[string][]byte{
			"rewst_agent.log.1": []byte(line), "rewst_agent.log.2.gz": gz(line),
			"rewst_agent.log.3.gz": gz(line),
		}, "MIN_COMPRESSED=1", "plain rotated copies are left: rewst_agent.log.1"},
		{"past the bound", map[string][]byte{
			"rewst_agent.log.1.gz": gz(line), "rewst_agent.log.3.gz": gz(line),
		}, "MIN_COMPRESSED=2", "past the retention bound of 2: rewst_agent.log.3.gz"},
		{"a temp file left", map[string][]byte{
			"rewst_agent.log.1.gz": gz(line), "rewst_agent.log.2.gz.tmp": []byte("half"),
		}, "MIN_COMPRESSED=1", "temp files are left"},
		{
			"not gzip",
			map[string][]byte{"rewst_agent.log.1.gz": []byte(line)},
			"MIN_COMPRESSED=1", "is not valid gzip",
		},
		{
			"not the agent's log",
			map[string][]byte{"rewst_agent.log.1.gz": gz("something else\n")},
			"MIN_COMPRESSED=1", "does not hold the agent's log lines",
		},
		{
			"no minimum",
			map[string][]byte{"rewst_agent.log.1.gz": gz(line)},
			"MIN_COMPRESSED=0", "MIN_COMPRESSED must be a positive integer",
		},
	} {
		code, out := assert(layout(tc.files), tc.env)
		if code == 0 || !strings.Contains(out, tc.says) {
			t.Errorf("%s: exit %d, want a failure saying %q\n%s", tc.name, code, tc.says, out)
		}
	}
}
