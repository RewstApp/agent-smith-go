//go:build windows

package agent

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

type windowsDefaultSystemInfoProvider struct{}

func (*windowsDefaultSystemInfoProvider) Hostname() (string, error) {
	return os.Hostname()
}

func (*windowsDefaultSystemInfoProvider) HostPlatform() (string, error) {
	hostStat, err := host.Info()
	if err != nil {
		return "", err
	}

	return hostStat.Platform, nil
}

func (*windowsDefaultSystemInfoProvider) CPUModelName() (string, error) {
	cpuStat, err := cpu.Info()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(cpuStat[0].ModelName), nil
}

func (*windowsDefaultSystemInfoProvider) TotalMemoryBytes() (uint64, error) {
	vmStat, err := mem.VirtualMemory()
	if err != nil {
		return 0, nil
	}

	return vmStat.Total, nil
}

func (*windowsDefaultSystemInfoProvider) MACAddress() (*string, error) {
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

func NewSystemInfoProvider() SystemInfoProvider {
	return &windowsDefaultSystemInfoProvider{}
}

type windowsDefaultDomainInfoProvider struct{}

func (*windowsDefaultDomainInfoProvider) ADDomain(ctx context.Context) (*string, error) {
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

func (*windowsDefaultDomainInfoProvider) IsADDomainController(ctx context.Context) (bool, error) {
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

func (*windowsDefaultDomainInfoProvider) IsEntraConnectServer() (bool, error) {
	entraServiceNames := []string{"ADSync", "Azure AD Sync", "EntraConnectSync", "OtherFutureName"}

	for _, name := range entraServiceNames {
		cmd := exec.Command("sc", "query", name)
		if err := cmd.Run(); err == nil {
			return true, nil
		}
	}

	return false, nil
}

func (*windowsDefaultDomainInfoProvider) EntraDomain(ctx context.Context) (*string, error) {
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

func NewDomainInfoProvider() DomainInfoProvider {
	return &windowsDefaultDomainInfoProvider{}
}
