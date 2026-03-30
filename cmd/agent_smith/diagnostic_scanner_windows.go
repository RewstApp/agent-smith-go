//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func getAgentDataRoot() string {
	return filepath.Join(os.Getenv("PROGRAMDATA"), "RewstRemoteAgent")
}

func formatServiceName(orgId string) string {
	return fmt.Sprintf("RewstRemoteAgent_%s", orgId)
}

// queryServiceStatus queries the Windows SCM via sc.exe.
// Returns (installed, running).
func queryServiceStatus(name string) (bool, bool) {
	out, err := exec.Command("sc", "query", name).CombinedOutput() // #nosec G204
	if err != nil {
		return false, false
	}
	output := string(out)
	// Service exists; check whether it is running
	return true, strings.Contains(output, "RUNNING")
}
