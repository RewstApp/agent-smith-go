//go:build windows

package main

import (
	"context"
	"errors"
	"testing"
)

// The SCM fallback scan takes the service names from the service manager's
// own enumeration and each service's state from the query every other check
// uses (sc-132999), keeping only the agent services named by a valid org id.
func TestFallbackScanAgents_ListsAgentServicesThroughTheManager(t *testing.T) {
	root := t.TempDir()
	const orgId = "43fb08b0-92cf-462a-924d-0b6be7a43a48"
	installAgentUnder(t, root, orgId)
	mgr := &mockServiceManager{
		services: []string{
			"Dnscache",
			"RewstRemoteAgent_" + orgId,
			"RewstRemoteAgent_" + orgId, // a duplicate is listed once
			"RewstRemoteAgent_not-an-org-id",
			"RewstRemoteAgent_5d0f4a60-1a1b-4f0e-9c3a-7c2f6c4b0e11", // no files under root
		},
		openService: &mockService{isActive: true},
	}

	agents := fallbackScanAgents(context.Background(), root, mgr)
	if len(agents) != 2 {
		t.Fatalf("got %d agents, want 2: %+v", len(agents), agents)
	}
	if agents[0].OrgId != orgId || !agents[0].IsRunning || agents[0].Device == nil {
		t.Errorf("the installed agent: %+v", agents[0])
	}
	if agents[0].ConfigFile != agentInfoIn(root, orgId).ConfigFile {
		t.Errorf("the entry's files are not under root: %s", agents[0].ConfigFile)
	}
	if agents[1].Device != nil || !agents[1].IsRunning {
		t.Errorf("the registered agent with no files under root: %+v", agents[1])
	}
}

func TestFallbackScanAgents_NothingWhenTheManagerCannotList(t *testing.T) {
	mgr := &mockServiceManager{listErr: errors.New("SCM wedged")}
	if agents := fallbackScanAgents(context.Background(), t.TempDir(), mgr); agents != nil {
		t.Errorf("got %+v, want nil", agents)
	}
}
