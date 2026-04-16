//go:build darwin

package agent

import (
	"context"
	"net"
	"os"
	"strings"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

var netInterfaces = net.Interfaces

type darwinDefaultSystemInfoProvider struct{}

func (*darwinDefaultSystemInfoProvider) Hostname() (string, error) {
	return os.Hostname()
}

func (*darwinDefaultSystemInfoProvider) HostPlatform() (string, error) {
	hostStat, err := host.Info()
	if err != nil {
		return "", err
	}

	return hostStat.Platform, nil
}

func (*darwinDefaultSystemInfoProvider) CPUModelName() (string, error) {
	cpuStat, err := cpu.Info()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(cpuStat[0].ModelName), nil
}

func (*darwinDefaultSystemInfoProvider) TotalMemoryBytes() (uint64, error) {
	vmStat, err := mem.VirtualMemory()
	if err != nil {
		return 0, nil
	}

	return vmStat.Total, nil
}

func (*darwinDefaultSystemInfoProvider) MACAddress() (*string, error) {
	ifas, err := netInterfaces()
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

	return nil, ErrNoMACAddress
}

func NewSystemInfoProvider() SystemInfoProvider {
	return &darwinDefaultSystemInfoProvider{}
}

type darwinDefaultDomainInfoProvider struct{}

func (*darwinDefaultDomainInfoProvider) ADDomain(context.Context) (*string, error) {
	return nil, nil
}

func (*darwinDefaultDomainInfoProvider) IsADDomainController(context.Context) (bool, error) {
	return false, nil
}

func (*darwinDefaultDomainInfoProvider) IsEntraConnectServer() (bool, error) {
	return false, nil
}

func (*darwinDefaultDomainInfoProvider) EntraDomain(context.Context) (*string, error) {
	return nil, nil
}

func NewDomainInfoProvider() DomainInfoProvider {
	return &darwinDefaultDomainInfoProvider{}
}
