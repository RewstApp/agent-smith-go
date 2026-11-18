//go:build linux

package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/utils"
)

// systemctlTimeout bounds every systemctl invocation the agent shells out to
// (start, stop, enable, disable, is-active, is-enabled, daemon-reload). These
// are one-shot commands, not the polled wait Windows' Stop() performs, but
// systemctl can still wedge - a D-Bus stall is a known real-world failure
// mode - and an unbounded exec.Command would then hang install, update,
// uninstall and config indefinitely: the same failure class already fixed for
// the Windows service Stop() wait. Five minutes matches serviceStopTimeout:
// generous enough to never cut off a legitimate stop, short enough that a
// wedged systemctl call fails with an actionable error in the same run rather
// than hanging forever.
const systemctlTimeout = 5 * time.Minute

// systemctlTimeoutOverrideStr is overridable via -ldflags for integration
// testing, the same mechanism stopTimeoutOverrideStr uses for the Windows
// service stop wait. Empty in production builds.
var systemctlTimeoutOverrideStr = ""

func resolveSystemctlTimeout() time.Duration {
	if systemctlTimeoutOverrideStr != "" {
		if d, err := time.ParseDuration(systemctlTimeoutOverrideStr); err == nil && d > 0 {
			return d
		}
	}
	return systemctlTimeout
}

type systemCtl interface {
	// Run runs systemctl for its effect: a failure's error is the output.
	Run(args ...string) error
	// Output runs systemctl under ctx for what it prints; a non-zero exit
	// returns the output with exec's error.
	Output(ctx context.Context, args ...string) ([]byte, error)
	ServiceConfigFilePath(name string) string
}

type defaultSystemCtl struct {
	// binary is the executable Run invokes. Empty selects "systemctl"; tests
	// point it at a fixture to exercise the timeout without depending on the
	// real systemd.
	binary string
}

func (s *defaultSystemCtl) Run(args ...string) error {
	out, err := s.Output(context.Background(), args...)
	if err != nil {
		return runFailure(out, err)
	}
	return nil
}

func (s *defaultSystemCtl) Output(ctx context.Context, args ...string) ([]byte, error) {
	binary := s.binary
	if binary == "" {
		binary = "systemctl"
	}
	return runOutput(ctx, "systemctl", binary, resolveSystemctlTimeout(), args...)
}

func (s *defaultSystemCtl) ServiceConfigFilePath(name string) string {
	return filepath.Join("/etc/systemd/system", fmt.Sprintf("%s.service", name))
}

type linuxService struct {
	name   string
	system systemCtl
}

func (linuxSvc *linuxService) Close() error {
	return nil
}

func (linuxSvc *linuxService) Start() error {
	return linuxSvc.system.Run("start", linuxSvc.name)
}

func (linuxSvc *linuxService) Stop() error {
	return linuxSvc.system.Run("stop", linuxSvc.name)
}

func (linuxSvc *linuxService) Delete() error {
	err := linuxSvc.system.Run("disable", linuxSvc.name)
	if err != nil {
		return err
	}

	// Delete the service configuration file
	return os.Remove(linuxSvc.system.ServiceConfigFilePath(linuxSvc.name))
}

func (linuxSvc *linuxService) IsActive() bool {
	status, err := linuxSvc.Status(context.Background())
	return err == nil && status.State == StateRunning
}

// Status reads the unit's LoadState, ActiveState and SubState in one
// `systemctl show` (sc-132999): active is running, activating, deactivating
// and reloading are pending, inactive and failed are stopped, and a unit
// systemd has never heard of is an error rather than a stopped service, since
// `show` answers for any name.
func (linuxSvc *linuxService) Status(ctx context.Context) (Status, error) {
	out, err := linuxSvc.system.Output(
		ctx, "show", linuxSvc.name,
		"--property=LoadState,ActiveState,SubState", "--no-pager",
	)
	if err != nil {
		return Status{}, fmt.Errorf("systemctl show %s: %w", linuxSvc.name, err)
	}
	props := parseUnitProperties(out)
	if props["LoadState"] == "not-found" {
		return Status{}, fmt.Errorf("unit %s is not loaded (LoadState=not-found)", linuxSvc.name)
	}
	active, sub := props["ActiveState"], props["SubState"]
	raw := active
	if sub != "" {
		raw = fmt.Sprintf("%s (%s)", active, sub)
	}
	return Status{State: unitState(active), Raw: raw}, nil
}

// Describe records `systemctl status` and `systemctl show` for the unit.
func (linuxSvc *linuxService) Describe(ctx context.Context) (string, error) {
	return describeWith(ctx, "systemctl", linuxSvc.system.Output, [][]string{
		{"status", linuxSvc.name, "--no-pager", "--full"},
		{"show", linuxSvc.name, "--no-pager"},
	})
}

// parseUnitProperties reads `systemctl show`'s Key=Value lines.
func parseUnitProperties(out []byte) map[string]string {
	props := map[string]string{}
	for line := range strings.SplitSeq(string(out), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			props[key] = value
		}
	}
	return props
}

// unitState classifies systemd's ActiveState.
func unitState(active string) State {
	switch active {
	case "active":
		return StateRunning
	case "inactive", "failed":
		return StateStopped
	case "activating", "deactivating", "reloading", "refreshing":
		return StatePending
	default:
		return StateUnknown
	}
}

type defaultServiceManager struct {
	system systemCtl
}

func (s *defaultServiceManager) Create(params AgentParams) (Service, error) {
	serviceConfig := strings.Builder{}

	fmt.Fprintf(&serviceConfig, "[Unit]\nDescription=%s\n\n", params.Name)
	fmt.Fprintf(
		&serviceConfig,
		"[Service]\nExecStart=%s --org-id %s --config-file %s --log-file %s\nRestart=always\n",
		params.AgentExecutablePath,
		params.OrgId,
		params.ConfigFilePath,
		params.LogFilePath,
	)
	if params.ServiceUsername != "" {
		fmt.Fprintf(
			&serviceConfig,
			"User=%s\nGroup=%s\n",
			params.ServiceUsername,
			params.ServiceUsername,
		)
	}
	fmt.Fprintf(&serviceConfig, "\n[Install]\nWantedBy=multi-user.target\n")

	serviceConfigFilePath := s.system.ServiceConfigFilePath(params.Name)
	err := os.WriteFile(serviceConfigFilePath, []byte(serviceConfig.String()), utils.DefaultFileMod)
	if err != nil {
		return nil, err
	}

	err = s.system.Run("daemon-reload")
	if err != nil {
		return nil, err
	}

	err = s.system.Run("enable", params.Name)
	if err != nil {
		return nil, err
	}

	return &linuxService{
		name:   params.Name,
		system: s.system,
	}, nil
}

func (s *defaultServiceManager) Open(name string) (Service, error) {
	// Use "is-enabled" instead of "status" to check if the service exists.
	// "status" fails for inactive services, but we only need to verify
	// the service is registered, not that it's currently running.
	err := s.system.Run("is-enabled", name)
	if err != nil {
		return nil, err
	}

	return &linuxService{
		name:   name,
		system: s.system,
	}, nil
}

func NewServiceManager() ServiceManager {
	return &defaultServiceManager{
		system: &defaultSystemCtl{},
	}
}
