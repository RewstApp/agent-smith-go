package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The three integration-build knobs added by sc-126322 fall back to their
// documented production values on anything but a valid override, so a typo in
// the build script cannot change a released agent's behaviour.

func TestResolveSpoolAttemptInterval(t *testing.T) {
	saved := spoolAttemptIntervalOverrideStr
	t.Cleanup(func() { spoolAttemptIntervalOverrideStr = saved })
	for name, tc := range map[string]struct {
		override string
		want     time.Duration
	}{
		"unset":     {"", 10 * time.Minute},
		"valid":     {"1s", time.Second},
		"malformed": {"soon", 10 * time.Minute},
		"zero":      {"0s", 10 * time.Minute},
		"negative":  {"-5s", 10 * time.Minute},
	} {
		spoolAttemptIntervalOverrideStr = tc.override
		if got := resolveSpoolAttemptInterval(); got != tc.want {
			t.Errorf("%s: resolveSpoolAttemptInterval() = %v, want %v", name, got, tc.want)
		}
	}
}

func TestResolveJournalMaxPending(t *testing.T) {
	saved := journalMaxPendingOverrideStr
	t.Cleanup(func() { journalMaxPendingOverrideStr = saved })
	for name, tc := range map[string]struct {
		override string
		want     int
	}{
		"unset":     {"", 1000},
		"valid":     {"20", 20},
		"malformed": {"many", 1000},
		"zero":      {"0", 1000},
		"negative":  {"-3", 1000},
	} {
		journalMaxPendingOverrideStr = tc.override
		if got := resolveJournalMaxPending(); got != tc.want {
			t.Errorf("%s: resolveJournalMaxPending() = %d, want %d", name, got, tc.want)
		}
	}
}

func TestResolveClockOffset(t *testing.T) {
	saved := clockOffsetOverrideFileStr
	t.Cleanup(func() { clockOffsetOverrideFileStr = saved })
	dir := t.TempDir()
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(
			filepath.Join(dir, "clock_offset_override"),
			[]byte(content),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}

	clockOffsetOverrideFileStr = ""
	write("25h")
	if got := resolveClockOffset(dir); got != 0 {
		t.Errorf("a build without the override read the file: got %v", got)
	}

	clockOffsetOverrideFileStr = "clock_offset_override"
	if got := resolveClockOffset(t.TempDir()); got != 0 {
		t.Errorf("missing file: got %v, want 0", got)
	}
	write("25h\n")
	if got := resolveClockOffset(dir); got != 25*time.Hour {
		t.Errorf("forward: got %v, want 25h", got)
	}
	write("-25h")
	if got := resolveClockOffset(dir); got != -25*time.Hour {
		t.Errorf("backward: got %v, want -25h", got)
	}
	write("yesterday")
	if got := resolveClockOffset(dir); got != 0 {
		t.Errorf("malformed: got %v, want 0", got)
	}
}
