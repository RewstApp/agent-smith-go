package agent

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/RewstApp/agent-smith-go/internal/version"
	"github.com/hashicorp/go-hclog"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
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

func (hostInfo *HostInfo) Load(ctx context.Context, orgId string, logger hclog.Logger) error {
	// Get stat objects
	hostStat, err := host.Info()
	if err != nil {
		return err
	}

	hostname, err := os.Hostname()
	if err != nil {
		return err
	}
	hostname = strings.ToLower(hostname)

	macAddress, err := getMacAddress()
	if err != nil {
		return err
	}

	cpuStat, err := cpu.Info()
	if err != nil {
		return err
	}

	vmStat, err := mem.VirtualMemory()
	if err != nil {
		return err
	}

	adDomain, err := getAdDomain(ctx)
	if err != nil {
		logger.Warn("Could not retrieve AD Domain", "error", err)
	}

	isAdDomainController, err := getIsAdDomainController(ctx)
	if err != nil {
		logger.Warn("Could not retrieve AD Domain Controller", "error", err)
	}

	isEntraConnectServer, err := getIsEntraConnectServer()
	if err != nil {
		logger.Warn("Could not retrieve Entra Connect Server", "error", err)
	}

	entraDomain, err := getEntraDomain(ctx)
	if err != nil {
		logger.Warn("Could not retrieve Entra Domain", "error", err)
	}

	agentExecutablePath := GetAgentExecutablePath(orgId)
	serviceExecutablePath := GetServiceExecutablePath(orgId)

	hostInfo.AgentVersion = version.Version
	hostInfo.AgentExecutablePath = agentExecutablePath
	hostInfo.ServiceExecutablePath = serviceExecutablePath
	hostInfo.HostName = hostname
	hostInfo.MacAddress = macAddress
	hostInfo.OperatingSystem = hostStat.Platform
	hostInfo.CpuModel = strings.TrimSpace(cpuStat[0].ModelName)
	hostInfo.RamGb = fmt.Sprintf("%d", vmStat.Total/1024/1024/1024)
	hostInfo.AdDomain = adDomain
	hostInfo.IsAdDomainController = isAdDomainController
	hostInfo.IsEntraConnectServer = isEntraConnectServer
	hostInfo.EntraDomain = entraDomain
	hostInfo.OrgId = orgId

	return nil
}
