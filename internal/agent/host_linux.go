//go:build linux

package agent

import (
	"context"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

type linuxDefaultSystemInfoProvider struct{}

func (*linuxDefaultSystemInfoProvider) Hostname() (string, error) {
	return os.Hostname()
}

func (*linuxDefaultSystemInfoProvider) HostPlatform() (string, error) {
	hostStat, err := host.Info()
	if err != nil {
		return "", err
	}

	return hostStat.Platform, nil
}

func (*linuxDefaultSystemInfoProvider) CPUModelName() (string, error) {
	cpuStat, err := cpu.Info()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(cpuStat[0].ModelName), nil
}

func (*linuxDefaultSystemInfoProvider) TotalMemoryBytes() (uint64, error) {
	vmStat, err := mem.VirtualMemory()
	if err != nil {
		return 0, nil
	}

	return vmStat.Total, nil
}

func (*linuxDefaultSystemInfoProvider) MACAddress() (*string, error) {
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
	return &linuxDefaultSystemInfoProvider{}
}

type linuxDefaultDomainInfoProvider struct{}

func (*linuxDefaultDomainInfoProvider) ADDomain(context.Context) (*string, error) {
	return nil, nil
}

func (*linuxDefaultDomainInfoProvider) IsADDomainController(context.Context) (bool, error) {
	return false, nil
}

func (*linuxDefaultDomainInfoProvider) IsEntraConnectServer() (bool, error) {
	return false, nil
}

func (*linuxDefaultDomainInfoProvider) EntraDomain(context.Context) (*string, error) {
	return nil, nil
}

func NewDomainInfoProvider() DomainInfoProvider {
	return &linuxDefaultDomainInfoProvider{}
}
