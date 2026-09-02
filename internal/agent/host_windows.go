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
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

var netInterfaces = net.Interfaces

// hostCommandTimeout bounds every OS-level command the domain info provider
// shells out to: the WMI queries the PowerShell runner issues (ADDomain,
// IsADDomainController), sc query (IsEntraConnectServer), and dsregcmd
// (EntraDomain). These run during config, install, update and diagnostic
// flows with a caller-supplied context that carries no deadline of its own
// (typically context.Background()), so without an internally enforced bound a
// wedged WMI repository, a stuck SCM, or a hung dsregcmd - all observed
// real-world failure modes, especially on domain controllers - would hang
// those flows indefinitely, the same failure class already fixed for the
// Windows service Stop() wait. Thirty seconds is generous for a command that
// normally completes in well under a second, while still failing fast enough
// that gathering host info for one field never meaningfully delays the
// caller.
const hostCommandTimeout = 30 * time.Second

// hostCommandTimeoutOverrideStr is overridable via -ldflags for integration
// testing, the same mechanism stopTimeoutOverrideStr uses for the Windows
// service stop wait. Empty in production builds.
var hostCommandTimeoutOverrideStr = ""

func resolveHostCommandTimeout() time.Duration {
	if hostCommandTimeoutOverrideStr != "" {
		if d, err := time.ParseDuration(hostCommandTimeoutOverrideStr); err == nil && d > 0 {
			return d
		}
	}
	return hostCommandTimeout
}

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
		return 0, err
	}

	return vmStat.Total, nil
}

func (*windowsDefaultSystemInfoProvider) MACAddress() (*string, error) {
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
	return &windowsDefaultSystemInfoProvider{}
}

type psRunnerFunc func(ctx context.Context, script string) (string, error)

func defaultPSRunner(ctx context.Context, script string) (string, error) {
	timeout := resolveHostCommandTimeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(
		ctx, "powershell", "-NoProfile", "-NonInteractive", "-Command", script,
	)
	var outb bytes.Buffer
	cmd.Stdout = &outb
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("powershell command timed out after %s: %w", timeout, err)
		}
		return "", err
	}
	return strings.TrimSpace(outb.String()), nil
}

type windowsDefaultDomainInfoProvider struct {
	psRunner psRunnerFunc
}

func (p *windowsDefaultDomainInfoProvider) ADDomain(ctx context.Context) (*string, error) {
	output, err := p.psRunner(ctx, `$domainInfo = (Get-WmiObject Win32_ComputerSystem).Domain
    if ($domainInfo -and $domainInfo -ne 'WORKGROUP') {
        return $domainInfo
    } else {
        return $null
    }`)
	if err != nil {
		return nil, err
	}
	if len(output) == 0 {
		return nil, nil
	}
	return &output, nil
}

func (p *windowsDefaultDomainInfoProvider) IsADDomainController(ctx context.Context) (bool, error) {
	output, err := p.psRunner(ctx, `$domainStatus = (Get-WmiObject Win32_ComputerSystem).DomainRole
    if ($domainStatus -eq 4 -or $domainStatus -eq 5) {
        return $true
    } else {
        return $false
    }`)
	if err != nil {
		return false, err
	}
	return output == "True", nil
}

func (p *windowsDefaultDomainInfoProvider) IsEntraConnectServer(ctx context.Context) (bool, error) {
	entraServiceNames := []string{"ADSync", "Azure AD Sync", "EntraConnectSync", "OtherFutureName"}

	for _, name := range entraServiceNames {
		queryCtx, cancel := context.WithTimeout(ctx, resolveHostCommandTimeout())
		cmd := exec.CommandContext(queryCtx, "sc", "query", name)
		err := cmd.Run()
		cancel()
		if err == nil {
			return true, nil
		}
	}

	return false, nil
}

func (p *windowsDefaultDomainInfoProvider) EntraDomain(ctx context.Context) (*string, error) {
	timeout := resolveHostCommandTimeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "dsregcmd", "/status")
	var outb bytes.Buffer
	cmd.Stdout = &outb

	err := cmd.Run()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("dsregcmd /status timed out after %s: %w", timeout, err)
		}
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
	return &windowsDefaultDomainInfoProvider{psRunner: defaultPSRunner}
}
