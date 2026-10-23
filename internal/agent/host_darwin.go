//go:build darwin

package agent

import (
	"context"
	"net"
	"os"
	"strings"
)

var netInterfaces = net.Interfaces

type darwinDefaultSystemInfoProvider struct {
	// queries holds the gopsutil calls; the zero value selects gopsutil.
	queries systemQueries
}

func (*darwinDefaultSystemInfoProvider) Hostname() (string, error) {
	return os.Hostname()
}

func (p *darwinDefaultSystemInfoProvider) HostPlatform(ctx context.Context) (string, error) {
	return hostPlatform(ctx, p.queries)
}

func (p *darwinDefaultSystemInfoProvider) CPUModelName(ctx context.Context) (string, error) {
	return cpuModelName(ctx, p.queries)
}

func (p *darwinDefaultSystemInfoProvider) TotalMemoryBytes(ctx context.Context) (uint64, error) {
	return totalMemoryBytes(ctx, p.queries)
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

func (*darwinDefaultDomainInfoProvider) IsEntraConnectServer(context.Context) (bool, error) {
	return false, nil
}

func (*darwinDefaultDomainInfoProvider) EntraDomain(context.Context) (*string, error) {
	return nil, nil
}

func NewDomainInfoProvider() DomainInfoProvider {
	return &darwinDefaultDomainInfoProvider{}
}
