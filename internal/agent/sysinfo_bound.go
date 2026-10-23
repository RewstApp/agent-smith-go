package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

// hostCommandTimeout bounds every OS-level query host-info gathering makes:
// on Windows the WMI queries the PowerShell runner issues (ADDomain,
// IsADDomainController), sc query (IsEntraConnectServer) and dsregcmd
// (EntraDomain); on every platform the system-info queries behind the
// SystemInfoProvider (host platform, CPU model, total memory), which gopsutil
// answers through WMI on Windows and sysctl / procfs elsewhere. These run
// during config, install, update, diagnostic and get_installation flows with a
// caller-supplied context that carries no deadline of its own (typically
// context.Background()), so without an internally enforced bound a wedged WMI
// repository, a stuck SCM or a hung dsregcmd - all observed real-world failure
// modes, especially on domain controllers - would hang those flows
// indefinitely, the same failure class already fixed for the Windows service
// Stop() wait. Thirty seconds is generous for queries that normally complete
// in well under a second, while still failing fast enough that gathering host
// info for one field never meaningfully delays the caller.
//
// Before sc-129668 the system-info queries were called with no context at all,
// and on Windows gopsutil then applied its own 3 s WMI timeout
// (common.Timeout): a cold runner or a domain controller with a slow WMI
// repository missed that window and the whole install aborted with "failed to
// read paths: context deadline exceeded", unretried, while the agent's own
// policy for the adjacent host-info shell-outs was to wait 30 s.
const hostCommandTimeout = 30 * time.Second

// hostCommandTimeoutOverrideStr is overridable via -ldflags for integration
// testing, the same mechanism stopTimeoutOverrideStr uses for the Windows
// service stop wait. Empty in production builds.
// Example: -ldflags "-X github.com/RewstApp/agent-smith-go/internal/agent.hostCommandTimeoutOverrideStr=5s"
var hostCommandTimeoutOverrideStr = ""

func resolveHostCommandTimeout() time.Duration {
	if hostCommandTimeoutOverrideStr != "" {
		if d, err := time.ParseDuration(hostCommandTimeoutOverrideStr); err == nil && d > 0 {
			return d
		}
	}
	return hostCommandTimeout
}

// systemQueries are the gopsutil calls the default system-info providers make,
// held as fields so a test can substitute a query that blocks or fails without
// depending on the machine it runs on. A zero value selects gopsutil.
type systemQueries struct {
	hostInfo func(ctx context.Context) (*host.InfoStat, error)
	cpuInfo  func(ctx context.Context) ([]cpu.InfoStat, error)
	memory   func(ctx context.Context) (*mem.VirtualMemoryStat, error)
}

// orDefault fills every unset query with gopsutil's context-aware call. The
// *WithContext variants matter: given a context that carries a deadline,
// gopsutil runs the query under it instead of its own 3 s default.
func (q systemQueries) orDefault() systemQueries {
	if q.hostInfo == nil {
		q.hostInfo = host.InfoWithContext
	}
	if q.cpuInfo == nil {
		q.cpuInfo = cpu.InfoWithContext
	}
	if q.memory == nil {
		q.memory = mem.VirtualMemoryWithContext
	}
	return q
}

// boundedQuery runs one system query under ctx, additionally bounded by
// hostCommandTimeout, and names the query and the bound when it is the bound
// (or the caller's cancellation) that ended it - so an aborted install says
// "host platform query timed out after 30s" rather than a bare "context
// deadline exceeded".
func boundedQuery[T any](
	ctx context.Context,
	what string,
	run func(context.Context) (T, error),
) (T, error) {
	timeout := resolveHostCommandTimeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	v, err := run(ctx)
	if err == nil {
		return v, nil
	}
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return v, fmt.Errorf("%s query timed out after %s: %w", what, timeout, err)
	case errors.Is(ctx.Err(), context.Canceled):
		return v, fmt.Errorf("%s query canceled: %w", what, err)
	}
	return v, err
}

// hostPlatform, cpuModelName and totalMemoryBytes are the shared bodies of
// the three platform providers' gopsutil-backed methods.
func hostPlatform(ctx context.Context, q systemQueries) (string, error) {
	stat, err := boundedQuery(ctx, "host platform", q.orDefault().hostInfo)
	if err != nil {
		return "", err
	}
	return stat.Platform, nil
}

func cpuModelName(ctx context.Context, q systemQueries) (string, error) {
	stats, err := boundedQuery(ctx, "cpu model", q.orDefault().cpuInfo)
	if err != nil {
		return "", err
	}
	if len(stats) == 0 {
		return "", errors.New("cpu model query returned no processors")
	}
	return strings.TrimSpace(stats[0].ModelName), nil
}

func totalMemoryBytes(ctx context.Context, q systemQueries) (uint64, error) {
	stat, err := boundedQuery(ctx, "total memory", q.orDefault().memory)
	if err != nil {
		return 0, err
	}
	return stat.Total, nil
}
