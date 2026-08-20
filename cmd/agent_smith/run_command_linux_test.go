//go:build linux

package main

import (
	"os"
	"strings"
	"testing"
)

func TestDetachedCommandWrapsInSystemdScope(t *testing.T) {
	cmd := detachedCommand("/opt/agent/rewst_agent_config.linux.bin", []string{"--update", "--org-id", "abc"}, os.Stdout, os.Stderr)

	wantPath := "systemd-run"
	if got := cmd.Path; got != wantPath && !strings.HasSuffix(got, "/"+wantPath) {
		t.Fatalf("Path = %q, want %q (or a resolved path ending in it)", got, wantPath)
	}

	wantArgs := []string{
		"systemd-run",
		"--scope",
		"--collect",
		"--quiet",
		"--",
		"/opt/agent/rewst_agent_config.linux.bin",
		"--update",
		"--org-id",
		"abc",
	}
	if len(cmd.Args) != len(wantArgs) {
		t.Fatalf("Args = %v, want %v", cmd.Args, wantArgs)
	}
	for i, want := range wantArgs {
		if cmd.Args[i] != want {
			t.Fatalf("Args[%d] = %q, want %q (full: %v)", i, cmd.Args[i], want, cmd.Args)
		}
	}

	if cmd.SysProcAttr == nil {
		t.Fatalf("SysProcAttr = nil, want non-nil")
	}
	if !cmd.SysProcAttr.Setsid {
		t.Fatalf("Setsid = false, want true")
	}
}

func endsWith(s, suffix string) bool {
	return len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix
}
