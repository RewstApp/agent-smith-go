package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/RewstApp/agent-smith-go/internal/version"
	"github.com/hashicorp/go-hclog"
)

type HostInfo struct {
	AgentVersion          string  `json:"agent_version"`
	AgentExecutablePath   string  `json:"agent_executable_path"`
	ServiceExecutablePath string  `json:"service_executable_path"`
	HostName              string  `json:"hostname"`
	MacAddress            *string `json:"mac_address"`
	OperatingSystem       string  `json:"operating_system"`
	CpuModel              string  `json:"cpu_model"`
	RamGb                 string  `json:"ram_gb"`
	AdDomain              *string `json:"ad_domain"`
	IsAdDomainController  bool    `json:"is_ad_domain_controller"`
	IsEntraConnectServer  bool    `json:"is_entra_connect_server"`
	EntraDomain           *string `json:"entra_domain"`
	OrgId                 string  `json:"org_id"`
}

type SystemInfoProvider interface {
	Hostname() (string, error)
	HostPlatform() (string, error)
	CPUModelName() (string, error)
	TotalMemoryBytes() (uint64, error)
	MACAddress() (*string, error)
}

type DomainInfoProvider interface {
	ADDomain(ctx context.Context) (*string, error)
	IsADDomainController(ctx context.Context) (bool, error)
	IsEntraConnectServer() (bool, error)
	EntraDomain(ctx context.Context) (*string, error)
}

func NewHostInfo(
	ctx context.Context,
	orgId string,
	logger hclog.Logger,
	sys SystemInfoProvider,
	domain DomainInfoProvider,
) (*HostInfo, error) {
	hostPlatform, err := sys.HostPlatform()
	if err != nil {
		return nil, err
	}

	cpuModelName, err := sys.CPUModelName()
	if err != nil {
		return nil, err
	}

	hostname, err := sys.Hostname()
	if err != nil {
		return nil, err
	}
	hostname = strings.ToLower(hostname)

	macAddress, err := sys.MACAddress()
	if err != nil {
		return nil, err
	}

	memoryBytes, err := sys.TotalMemoryBytes()
	if err != nil {
		return nil, err
	}

	adDomain, err := domain.ADDomain(ctx)
	if err != nil {
		logger.Warn("Could not retrieve AD Domain", "error", err)
	}

	isAdDomainController, err := domain.IsADDomainController(ctx)
	if err != nil {
		logger.Warn("Could not retrieve AD Domain Controller", "error", err)
	}

	isEntraConnectServer, err := domain.IsEntraConnectServer()
	if err != nil {
		logger.Warn("Could not retrieve Entra Connect Server", "error", err)
	}

	entraDomain, err := domain.EntraDomain(ctx)
	if err != nil {
		logger.Warn("Could not retrieve Entra Domain", "error", err)
	}

	agentExecutablePath := GetAgentExecutablePath(orgId)
	serviceExecutablePath := GetServiceExecutablePath(orgId)

	var hostInfo HostInfo
	hostInfo.AgentVersion = version.Version
	hostInfo.AgentExecutablePath = agentExecutablePath
	hostInfo.ServiceExecutablePath = serviceExecutablePath
	hostInfo.HostName = hostname
	hostInfo.MacAddress = macAddress
	hostInfo.OperatingSystem = hostPlatform
	hostInfo.CpuModel = cpuModelName
	hostInfo.RamGb = fmt.Sprintf("%d", memoryBytes/1024/1024/1024)
	hostInfo.AdDomain = adDomain
	hostInfo.IsAdDomainController = isAdDomainController
	hostInfo.IsEntraConnectServer = isEntraConnectServer
	hostInfo.EntraDomain = entraDomain
	hostInfo.OrgId = orgId

	return &hostInfo, nil
}
