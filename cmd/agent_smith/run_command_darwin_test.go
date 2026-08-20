//go:build darwin

package main

import (
	"os"
	"testing"
)

func TestDetachedCommandRunsDirectlyOnDarwin(t *testing.T) {
	path := "/opt/agent/rewst_agent_config.mac-os.bin"
	args := []string{"--update", "--org-id", "abc"}
	cmd := detachedCommand(path, args, os.Stdout, os.Stderr)

	if cmd.Path != path {
		t.Fatalf("Path = %q, want %q", cmd.Path, path)
	}
	wantArgs := append([]string{path}, args...)
	if len(cmd.Args) != len(wantArgs) {
		t.Fatalf("Args = %v, want %v", cmd.Args, wantArgs)
	}
	for i, want := range wantArgs {
		if cmd.Args[i] != want {
			t.Fatalf("Args[%d] = %q, want %q", i, cmd.Args[i], want)
		}
	}

	if cmd.SysProcAttr == nil {
		t.Fatalf("SysProcAttr = nil, want non-nil")
	}
	if !cmd.SysProcAttr.Setsid {
		t.Fatalf("Setsid = false, want true")
	}
}
