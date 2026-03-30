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

// queryServiceStatus checks the plist file for installation and launchctl list
// for running state. Returns (installed, running).
func queryServiceStatus(name string) (bool, bool) {
	plistPath := filepath.Join("/Library/LaunchDaemons", fmt.Sprintf("%s.plist", name))
	if _, err := os.Stat(plistPath); err != nil {
		return false, false
	}

	// "launchctl list <name>" prints a tab-separated line: PID  LastExit  Label
	// If PID is non-zero the service is running.
	out, err := exec.Command("launchctl", "list", name).CombinedOutput() // #nosec G204
	if err != nil {
		// Plist exists but not loaded — installed, stopped
		return true, false
	}

	parts := strings.Fields(string(out))
	if len(parts) >= 1 && parts[0] != "-" && parts[0] != "0" {
		return true, true
	}
	return true, false
}
