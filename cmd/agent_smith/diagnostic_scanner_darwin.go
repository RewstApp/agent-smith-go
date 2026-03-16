//go:build darwin

package main

import (
	"fmt"
	"path/filepath"
)

func getAgentDataRoot() string {
	return filepath.Join("/Library", "Application Support", "rewst_remote_agent")
}

func getServiceNamePrefix() string {
	return "io.rewst.remote_agent_"
}

func formatServiceName(orgId string) string {
	return fmt.Sprintf("io.rewst.remote_agent_%s", orgId)
}
