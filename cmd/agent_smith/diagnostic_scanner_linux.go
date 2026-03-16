//go:build linux

package main

import (
	"fmt"
	"path/filepath"
)

func getAgentDataRoot() string {
	return filepath.Join("/etc", "rewst_remote_agent")
}

func getServiceNamePrefix() string {
	return "rewst_remote_agent_"
}

func formatServiceName(orgId string) string {
	return fmt.Sprintf("rewst_remote_agent_%s", orgId)
}
