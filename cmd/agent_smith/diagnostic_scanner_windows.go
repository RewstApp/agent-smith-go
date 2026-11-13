//go:build windows

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/RewstApp/agent-smith-go/internal/agent"
)

func formatServiceName(orgId string) string {
	return fmt.Sprintf("RewstRemoteAgent_%s", orgId)
}

// fallbackScanAgents queries the Windows SCM for services named
// RewstRemoteAgent_{uuid} and builds agentInfo entries from them.
// This handles the case where the data-directory scan finds nothing (the
// directory cannot be read in the current process context, or the agent was
// installed under a PROGRAMDATA that differs from this process's); the
// entries' paths are still under root, the one place the rest of the
// diagnostic reads from.
func fallbackScanAgents(root string) []agentInfo {
	out, err := diagnosticCommand(
		"sc", "query", "type=", "service", "state=", "all", "bufsize=", "65536",
	)
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

		info := agentInfoIn(root, orgId)
		if configBytes, err := os.ReadFile(info.ConfigFile); err == nil {
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

// queryServiceStatus queries the Windows SCM via sc.exe, bounded by
// diagnosticCommandTimeout. Returns (installed, running).
func queryServiceStatus(name string) (bool, bool) {
	out, err := diagnosticCommand("sc", "query", name)
	if err != nil {
		return false, false
	}
	output := string(out)
	// Service exists; check whether it is running
	return true, strings.Contains(output, "RUNNING")
}
