package agent

import (
	"context"

	"github.com/hashicorp/go-hclog"
)

type PathsData struct {
	ServiceExecutablePath string    `json:"service_executable_path"`
	AgentExecutablePath   string    `json:"agent_executable_path"`
	ConfigFilePath        string    `json:"config_file_path"`
	ServiceManagerPath    string    `json:"service_manager_path"`
	Tags                  *HostInfo `json:"tags"`
}

func NewPathsData(
	ctx context.Context,
	orgId string,
	logger hclog.Logger,
	sys SystemInfoProvider,
	domain DomainInfoProvider,
) (*PathsData, error) {
	var paths PathsData

	paths.ServiceExecutablePath = GetServiceExecutablePath(orgId)
	paths.AgentExecutablePath = GetAgentExecutablePath(orgId)
	paths.ConfigFilePath = GetConfigFilePath(orgId)
	paths.ServiceManagerPath = GetServiceManagerPath(orgId)

	hostInfo, err := NewHostInfo(ctx, orgId, logger, sys, domain)
	if err != nil {
		return nil, err
	}

	paths.Tags = hostInfo

	return &paths, nil
}
