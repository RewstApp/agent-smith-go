package agent

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strings"

	"github.com/RewstApp/agent-smith-go/internal/version"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/winservices"
)

type HostInfo struct {
	AgentVersion          string  `json:"agent_version"`
	AgentExecutablePath   string  `json:"agent_executable_path"`
	AgentApp              string  `json:"agent_app"`
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

func getAdDomain(ctx context.Context) (*string, error) {
	if runtime.GOOS != "windows" {
		return nil, nil
	}

	cmd := exec.CommandContext(ctx, "powershell", "-Command", `$domainInfo = (Get-WmiObject Win32_ComputerSystem).Domain
    if ($domainInfo -and $domainInfo -ne 'WORKGROUP') {
        return $domainInfo
    } else {
        return $null
    }`)

	var outb bytes.Buffer
	cmd.Stdout = &outb

	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	domain := strings.TrimSpace(outb.String())
	if len(domain) == 0 {
		return nil, nil
	}

	return &domain, nil
}

func getIsAdDomainController(ctx context.Context) (bool, error) {
	if runtime.GOOS != "windows" {
		return false, nil
	}

	cmd := exec.CommandContext(ctx, "powershell", "-Command", `$domainStatus = (Get-WmiObject Win32_ComputerSystem).DomainRole
    if ($domainStatus -eq 4 -or $domainStatus -eq 5) {
        return $true
    } else {
        return $false
    }`)

	var outb bytes.Buffer
	cmd.Stdout = &outb

	err := cmd.Run()
	if err != nil {
		return false, err
	}

	return strings.TrimSpace(outb.String()) == "True", nil
}

func getIsEntraConnectServer() (bool, error) {
	if runtime.GOOS != "windows" {
		return false, nil
	}

	entraServiceNames := []string{"ADSync", "Azure AD Sync", "EntraConnectSync", "OtherFutureName"}

	services, err := winservices.ListServices()
	if err != nil {
		return false, err
	}

	for _, service := range services {
		for _, entraServiceName := range entraServiceNames {
			if strings.EqualFold(service.Name, entraServiceName) {
				return true, nil
			}
		}
	}

	return false, nil
}

func getMacAddress() (*string, error) {
	ifas, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	for _, ifa := range ifas {
		a := ifa.HardwareAddr.String()
		if len(a) > 0 {
			// Replace : with empty string
			a = strings.ReplaceAll(a, ":", "")
			return &a, nil
		}
	}

	return nil, fmt.Errorf("%s", "No mac address found")
}

func getEntraDomain(ctx context.Context) (*string, error) {
	if runtime.GOOS != "windows" {
		return nil, nil
	}

	cmd := exec.CommandContext(ctx, "dsregcmd", "/status")
	var outb bytes.Buffer
	cmd.Stdout = &outb

	err := cmd.Run()
	if err != nil {
		return nil, err
	}

	output := outb.String()

	azureAdJoined := false
	domain := ""

	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "AzureAdJoined") && strings.Contains(line, "YES") {
			azureAdJoined = true
		}

		if strings.Contains(line, "DomainName") {
			domain = strings.TrimSpace(strings.Split(line, ":")[1])

			if azureAdJoined {
				return &domain, nil
			}
		}
	}

	return nil, nil
}

func (hostInfo *HostInfo) Load(ctx context.Context, orgId string) error {
	// Get stat objects
	hostStat, err := host.Info()
	if err != nil {
		return err
	}

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
		return err
	}

	isAdDomainController, err := getIsAdDomainController(ctx)
	if err != nil {
		return err
	}

	isEntraConnectServer, err := getIsEntraConnectServer()
	if err != nil {
		return err
	}

	entraDomain, err := getEntraDomain(ctx)
	if err != nil {
		return err
	}

	agentExecutablePath, err := GetAgentExecutablePath(orgId)
	if err != nil {
		return err
	}

	serviceExecutablePath, err := GetServiceExecutablePath(orgId)
	if err != nil {
		return err
	}

	hostInfo.AgentVersion = version.Version
	hostInfo.AgentExecutablePath = agentExecutablePath
	hostInfo.ServiceExecutablePath = serviceExecutablePath
	hostInfo.HostName = hostStat.Hostname
	hostInfo.MacAddress = macAddress
	hostInfo.OperatingSystem = hostStat.Platform
	hostInfo.CpuModel = strings.TrimSpace(cpuStat[0].ModelName)
	hostInfo.RamGb = fmt.Sprintf("%d", vmStat.Total/1024/1024/1024)
	hostInfo.AdDomain = adDomain
	hostInfo.IsAdDomainController = isAdDomainController
	hostInfo.IsEntraConnectServer = isEntraConnectServer
	hostInfo.EntraDomain = entraDomain
	hostInfo.OrgId = orgId
	hostInfo.AgentApp = "agent-smith-go"

	return nil
}
