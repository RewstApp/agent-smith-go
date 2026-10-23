//go:build linux

package agent

import (
	"context"
	"net"
	"os"
	"strings"
)

var netInterfaces = net.Interfaces

type linuxDefaultSystemInfoProvider struct {
	// queries holds the gopsutil calls; the zero value selects gopsutil.
	queries systemQueries
}

func (*linuxDefaultSystemInfoProvider) Hostname() (string, error) {
	return os.Hostname()
}

func (p *linuxDefaultSystemInfoProvider) HostPlatform(ctx context.Context) (string, error) {
	return hostPlatform(ctx, p.queries)
}

func (p *linuxDefaultSystemInfoProvider) CPUModelName(ctx context.Context) (string, error) {
	return cpuModelName(ctx, p.queries)
}

func (p *linuxDefaultSystemInfoProvider) TotalMemoryBytes(ctx context.Context) (uint64, error) {
	return totalMemoryBytes(ctx, p.queries)
}

func (*linuxDefaultSystemInfoProvider) MACAddress() (*string, error) {
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
	return &linuxDefaultSystemInfoProvider{}
}

type linuxDefaultDomainInfoProvider struct{}

func (*linuxDefaultDomainInfoProvider) ADDomain(context.Context) (*string, error) {
	return nil, nil
}

func (*linuxDefaultDomainInfoProvider) IsADDomainController(context.Context) (bool, error) {
	return false, nil
}

func (*linuxDefaultDomainInfoProvider) IsEntraConnectServer(context.Context) (bool, error) {
	return false, nil
}

func (*linuxDefaultDomainInfoProvider) EntraDomain(context.Context) (*string, error) {
	return nil, nil
}

func NewDomainInfoProvider() DomainInfoProvider {
	return &linuxDefaultDomainInfoProvider{}
}
