//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func getAgentDataRoot() string {
	return filepath.Join("/Library", "Application Support", "rewst_remote_agent")
}

func formatServiceName(orgId string) string {
	return fmt.Sprintf("io.rewst.remote_agent_%s", orgId)
}

// queryServiceStatus checks the plist file for installation and launchctl print
// for running state. Returns (installed, running).
func queryServiceStatus(name string) (bool, bool) {
	plistPath := filepath.Join("/Library/LaunchDaemons", fmt.Sprintf("%s.plist", name))
	if _, err := os.Stat(plistPath); err != nil {
		return false, false
	}

	// "launchctl print system/<name>" outputs a plist-style dict; parse the
	// "state = running" line — consistent with IsActive() in service_darwin.go.
	out, err := exec.Command("launchctl", "print", fmt.Sprintf("system/%s", name)).CombinedOutput() // #nosec G204
	if err != nil {
		// Plist exists but service is not loaded — installed, stopped
		return true, false
	}

	for _, line := range strings.Split(string(out), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "=", 2)
		if len(parts) == 2 && strings.TrimSpace(parts[0]) == "state" {
			return true, strings.TrimSpace(parts[1]) == "running"
		}
	}
	return true, false
}
