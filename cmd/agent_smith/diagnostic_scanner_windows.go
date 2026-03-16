//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
)

func getAgentDataRoot() string {
	return filepath.Join(os.Getenv("PROGRAMDATA"), "RewstRemoteAgent")
}

func getServiceNamePrefix() string {
	return "RewstRemoteAgent_"
}

func formatServiceName(orgId string) string {
	return fmt.Sprintf("RewstRemoteAgent_%s", orgId)
}
