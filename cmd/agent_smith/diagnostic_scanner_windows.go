//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/service"
)

func formatServiceName(orgId string) string {
	return fmt.Sprintf("RewstRemoteAgent_%s", orgId)
}

// fallbackScanAgents asks the Windows SCM for services named
// RewstRemoteAgent_{uuid} and builds agentInfo entries from them. This
// handles the case where the data-directory scan finds nothing (the
// directory cannot be read in the current process context, or the agent was
// installed under a PROGRAMDATA that differs from this process's); the
// entries' paths are still under root, the one place the rest of the
// diagnostic reads from. The names come from the service manager's own
// enumeration (service.ServiceLister) and each service's state from the
// same query every other check uses (sc-132999); before that this parsed
// `sc query` on its own.
func fallbackScanAgents(ctx context.Context, root string, mgr service.ServiceManager) []agentInfo {
	lister, ok := mgr.(service.ServiceLister)
	if !ok {
		return nil
	}
	names, err := lister.ListServices()
	if err != nil {
		return nil
	}

	const svcPrefix = "RewstRemoteAgent_"
	seen := make(map[string]bool)
	var agents []agentInfo

	for _, svcName := range names {
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

		info.IsRunning = serviceRunning(ctx, mgr, info.ServiceName)
		agents = append(agents, info)
	}
	return agents
}
