//go:build linux

package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

func getAgentDataRoot() string {
	return filepath.Join("/etc", "rewst_remote_agent")
}

func formatServiceName(orgId string) string {
	return fmt.Sprintf("rewst_remote_agent_%s", orgId)
}

// queryServiceStatus queries systemd for both existence and active state.
// Returns (installed, running).
func queryServiceStatus(name string) (bool, bool) {
	// Use "show" to get LoadState and ActiveState in one call — works for any
	// service regardless of whether it is currently running.
	out, err := exec.Command( // #nosec G204
		"systemctl", "show", name,
		"--property=LoadState,ActiveState",
		"--no-pager",
	).CombinedOutput()
	if err != nil {
		return false, false
	}

	output := string(out)
	installed := strings.Contains(output, "LoadState=loaded")
	running := strings.Contains(output, "ActiveState=active")
	return installed, running
}
