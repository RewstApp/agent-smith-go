//go:build linux

package main

import (
	"context"
	"fmt"

	"github.com/RewstApp/agent-smith-go/internal/service"
)

func formatServiceName(orgId string) string {
	return fmt.Sprintf("rewst_remote_agent_%s", orgId)
}

// fallbackScanAgents has nothing to add on Linux: the data root is the one
// place an agent is installed.
func fallbackScanAgents(context.Context, string, service.ServiceManager) []agentInfo {
	return nil
}
