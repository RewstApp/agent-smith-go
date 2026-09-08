//go:build windows

package agent

import (
	"bytes"
	"context"
	"errors"
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

// scQueryFunc runs "sc query <name>" and reports whether the service exists,
// returning nil when the query succeeds and an error otherwise. It is a field
// on the provider - like psRunnerFunc - so tests can substitute a query that
// hangs or fails deterministically instead of depending on the real Service
// Control Manager's state.
type scQueryFunc func(ctx context.Context, name string) error

func defaultSCQuery(ctx context.Context, name string) error {
	return exec.CommandContext(ctx, "sc", "query", name).Run()
}

type windowsDefaultDomainInfoProvider struct {
	psRunner psRunnerFunc
	scQuery  scQueryFunc
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

// IsEntraConnectServer probes for an Entra Connect sync service by name. The
// whole check - up to four sequential "sc query" calls - shares a single
// hostCommandTimeout budget rather than granting each call its own, so a
// wedged Service Control Manager cannot stretch one host-info field out to
// four times the documented bound (two minutes at the production timeout).
// That matters most on the get_installation path, where the caller is an MQTT
// command-processing worker out of a small pool and is not covered by the
// per-command execution timeout, which bounds only the interpreter's own
// command execution.
//
// A query that fails because the service simply is not installed (sc exits
// 1060) is the expected case for a non-Entra-Connect host and just moves on to
// the next name. A query that fails with the shared budget expired or the
// caller canceled is different in kind: the failure says nothing about whether
// the service exists, and every later name would fail the same way, so the
// check aborts with an error instead of reporting a host as "not an Entra
// Connect server" on the strength of a timeout.
func (p *windowsDefaultDomainInfoProvider) IsEntraConnectServer(ctx context.Context) (bool, error) {
	entraServiceNames := []string{"ADSync", "Azure AD Sync", "EntraConnectSync", "OtherFutureName"}

	timeout := resolveHostCommandTimeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for _, name := range entraServiceNames {
		err := p.scQuery(ctx, name)
		if err == nil {
			return true, nil
		}

		if ctxErr := ctx.Err(); ctxErr != nil {
			if errors.Is(ctxErr, context.DeadlineExceeded) {
				return false, fmt.Errorf(
					"sc query %q timed out after %s: %w", name, timeout, err,
				)
			}
			return false, fmt.Errorf("sc query %q aborted: %w", name, ctxErr)
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
	return &windowsDefaultDomainInfoProvider{
		psRunner: defaultPSRunner,
		scQuery:  defaultSCQuery,
	}
}
