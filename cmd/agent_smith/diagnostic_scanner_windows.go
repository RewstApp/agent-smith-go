//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/RewstApp/agent-smith-go/internal/agent"
)

func getAgentDataRoot() string {
	programData := os.Getenv("PROGRAMDATA")
	if programData == "" {
		programData = `C:\ProgramData`
	}
	return filepath.Join(programData, "RewstRemoteAgent")
}

func formatServiceName(orgId string) string {
	return fmt.Sprintf("RewstRemoteAgent_%s", orgId)
}

// fallbackScanAgents queries the Windows SCM for services named
// RewstRemoteAgent_{uuid} and builds agentInfo entries from them.
// This handles the case where the data-directory scan finds nothing
// (e.g. when PROGRAMDATA was unset during installation or the directory
// cannot be read in the current process context).
func fallbackScanAgents(root string) []agentInfo {
	out, err := exec.Command("sc", "query", "type=", "service", "state=", "all", "bufsize=", "65536").CombinedOutput() // #nosec G204
	if err != nil {
		return nil
	}

	const svcPrefix = "RewstRemoteAgent_"
	seen := make(map[string]bool)
	var agents []agentInfo

	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "SERVICE_NAME: ") {
			continue
		}
		svcName := strings.TrimSpace(strings.TrimPrefix(line, "SERVICE_NAME: "))
		if !strings.HasPrefix(svcName, svcPrefix) {
			continue
		}
		orgId := strings.TrimPrefix(svcName, svcPrefix)
		if !isValidOrgId(orgId) || seen[orgId] {
			continue
		}
		seen[orgId] = true

		info := agentInfo{
			OrgId:       orgId,
			ConfigFile:  agent.GetConfigFilePath(orgId),
			LogFile:     agent.GetLogFilePath(orgId),
			ServiceName: formatServiceName(orgId),
		}

		configPath := filepath.Join(root, orgId, "config.json")
		if configBytes, err := os.ReadFile(configPath); err == nil {
			var device agent.Device
			if json.Unmarshal(configBytes, &device) == nil {
				info.Device = &device
			}
		}

		_, info.IsRunning = queryServiceStatus(info.ServiceName)
		agents = append(agents, info)
	}
	return agents
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
