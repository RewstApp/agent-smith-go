package agent

import (
	"context"
	"testing"

	"github.com/hashicorp/go-hclog"
)

func TestHostInfo_Load(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping host info load test in short mode")
	}

	var hostInfo HostInfo
	logger := hclog.NewNullLogger()
	orgId := "test-org"

	err := hostInfo.Load(context.Background(), orgId, logger)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Verify required fields are populated
	if hostInfo.AgentVersion == "" {
		t.Error("expected AgentVersion to be populated")
	}

	if hostInfo.AgentExecutablePath == "" {
		t.Error("expected AgentExecutablePath to be populated")
	}

	if hostInfo.ServiceExecutablePath == "" {
		t.Error("expected ServiceExecutablePath to be populated")
	}

	if hostInfo.HostName == "" {
		t.Error("expected HostName to be populated")
	}

	if hostInfo.MacAddress == nil {
		t.Error("expected MacAddress to be populated")
	}

	if hostInfo.OperatingSystem == "" {
		t.Error("expected OperatingSystem to be populated")
	}

	if hostInfo.CpuModel == "" {
		t.Error("expected CpuModel to be populated")
	}

	if hostInfo.RamGb == "" {
		t.Error("expected RamGb to be populated")
	}

	if hostInfo.OrgId != orgId {
		t.Errorf("expected OrgId %s, got %s", orgId, hostInfo.OrgId)
	}

	t.Logf("HostInfo loaded successfully:")
	t.Logf("  AgentVersion: %s", hostInfo.AgentVersion)
	t.Logf("  HostName: %s", hostInfo.HostName)
	t.Logf("  MacAddress: %v", *hostInfo.MacAddress)
	t.Logf("  OperatingSystem: %s", hostInfo.OperatingSystem)
	t.Logf("  CpuModel: %s", hostInfo.CpuModel)
	t.Logf("  RamGb: %s", hostInfo.RamGb)
	t.Logf("  AdDomain: %v", hostInfo.AdDomain)
	t.Logf("  IsAdDomainController: %v", hostInfo.IsAdDomainController)
	t.Logf("  IsEntraConnectServer: %v", hostInfo.IsEntraConnectServer)
	t.Logf("  EntraDomain: %v", hostInfo.EntraDomain)
}

func TestHostInfo_Load_WithDebugLogger(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping host info load test in short mode")
	}

	var hostInfo HostInfo
	logger := hclog.New(&hclog.LoggerOptions{
		Name:  "test",
		Level: hclog.Debug,
	})
	orgId := "test-org-debug"

	err := hostInfo.Load(context.Background(), orgId, logger)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if hostInfo.OrgId != orgId {
		t.Errorf("expected OrgId %s, got %s", orgId, hostInfo.OrgId)
	}
}

func TestHostInfo_Load_CancelledContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping host info load test in short mode")
	}

	var hostInfo HostInfo
	logger := hclog.NewNullLogger()
	orgId := "test-org"

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := hostInfo.Load(ctx, orgId, logger)
	// May succeed or fail depending on timing
	if err != nil {
		t.Logf("Load() with cancelled context returned error (may be expected): %v", err)
	} else {
		t.Log("Load() completed before cancellation")
	}
}

func TestHostInfo_Load_DifferentOrgIds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping host info load test in short mode")
	}

	tests := []struct {
		name  string
		orgId string
	}{
		{
			name:  "standard org id",
			orgId: "test-org-123",
		},
		{
			name:  "org with underscores",
			orgId: "test_org_456",
		},
		{
			name:  "numeric org id",
			orgId: "789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var hostInfo HostInfo
			logger := hclog.NewNullLogger()

			err := hostInfo.Load(context.Background(), tt.orgId, logger)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}

			if hostInfo.OrgId != tt.orgId {
				t.Errorf("expected OrgId %s, got %s", tt.orgId, hostInfo.OrgId)
			}

			// Verify paths include the org ID
			if hostInfo.AgentExecutablePath != GetAgentExecutablePath(tt.orgId) {
				t.Errorf("AgentExecutablePath mismatch")
			}

			if hostInfo.ServiceExecutablePath != GetServiceExecutablePath(tt.orgId) {
				t.Errorf("ServiceExecutablePath mismatch")
			}
		})
	}
}

func TestPathsData_Load(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping paths data load test in short mode")
	}

	var pathsData PathsData
	logger := hclog.NewNullLogger()
	orgId := "test-org"

	err := pathsData.Load(context.Background(), orgId, logger)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	// Verify all paths are populated
	if pathsData.ServiceExecutablePath == "" {
		t.Error("expected ServiceExecutablePath to be populated")
	}

	if pathsData.AgentExecutablePath == "" {
		t.Error("expected AgentExecutablePath to be populated")
	}

	if pathsData.ConfigFilePath == "" {
		t.Error("expected ConfigFilePath to be populated")
	}

	if pathsData.ServiceManagerPath == "" {
		t.Error("expected ServiceManagerPath to be populated")
	}

	// Verify paths match expected values
	if pathsData.ServiceExecutablePath != GetServiceExecutablePath(orgId) {
		t.Errorf("ServiceExecutablePath mismatch")
	}

	if pathsData.AgentExecutablePath != GetAgentExecutablePath(orgId) {
		t.Errorf("AgentExecutablePath mismatch")
	}

	if pathsData.ConfigFilePath != GetConfigFilePath(orgId) {
		t.Errorf("ConfigFilePath mismatch")
	}

	if pathsData.ServiceManagerPath != GetServiceManagerPath(orgId) {
		t.Errorf("ServiceManagerPath mismatch")
	}

	// Verify Tags (HostInfo) is also populated
	if pathsData.Tags.OrgId != orgId {
		t.Errorf("expected Tags.OrgId %s, got %s", orgId, pathsData.Tags.OrgId)
	}

	if pathsData.Tags.HostName == "" {
		t.Error("expected Tags.HostName to be populated")
	}

	t.Logf("PathsData loaded successfully:")
	t.Logf("  ServiceExecutablePath: %s", pathsData.ServiceExecutablePath)
	t.Logf("  AgentExecutablePath: %s", pathsData.AgentExecutablePath)
	t.Logf("  ConfigFilePath: %s", pathsData.ConfigFilePath)
	t.Logf("  ServiceManagerPath: %s", pathsData.ServiceManagerPath)
	t.Logf("  Tags.HostName: %s", pathsData.Tags.HostName)
}

func TestPathsData_Load_CancelledContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping paths data load test in short mode")
	}

	var pathsData PathsData
	logger := hclog.NewNullLogger()
	orgId := "test-org"

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	err := pathsData.Load(ctx, orgId, logger)
	// May succeed or fail depending on timing
	if err != nil {
		t.Logf("Load() with cancelled context returned error (may be expected): %v", err)
	} else {
		t.Log("Load() completed before cancellation")
	}
}

func TestPathsData_Load_MultipleOrgIds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping paths data load test in short mode")
	}

	orgIds := []string{"org1", "org2", "org3"}

	for _, orgId := range orgIds {
		t.Run(orgId, func(t *testing.T) {
			var pathsData PathsData
			logger := hclog.NewNullLogger()

			err := pathsData.Load(context.Background(), orgId, logger)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}

			// Verify all path functions were called correctly
			if pathsData.ServiceExecutablePath != GetServiceExecutablePath(orgId) {
				t.Error("ServiceExecutablePath does not match expected value")
			}

			if pathsData.AgentExecutablePath != GetAgentExecutablePath(orgId) {
				t.Error("AgentExecutablePath does not match expected value")
			}

			if pathsData.ConfigFilePath != GetConfigFilePath(orgId) {
				t.Error("ConfigFilePath does not match expected value")
			}

			if pathsData.ServiceManagerPath != GetServiceManagerPath(orgId) {
				t.Error("ServiceManagerPath does not match expected value")
			}

			if pathsData.Tags.OrgId != orgId {
				t.Errorf("Tags.OrgId mismatch: expected %s, got %s", orgId, pathsData.Tags.OrgId)
			}
		})
	}
}
