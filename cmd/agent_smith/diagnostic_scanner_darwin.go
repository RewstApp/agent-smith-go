//go:build darwin

package main

import (
	"context"
	"fmt"

	"github.com/RewstApp/agent-smith-go/internal/service"
)

func formatServiceName(orgId string) string {
	return fmt.Sprintf("io.rewst.remote_agent_%s", orgId)
}

// fallbackScanAgents has nothing to add on macOS: the data root is the one
// place an agent is installed.
func fallbackScanAgents(context.Context, string, service.ServiceManager) []agentInfo {
	return nil
}
