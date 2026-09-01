//go:build linux

package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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
	Run(args ...string) error
	ServiceConfigFilePath(name string) string
}

type defaultSystemCtl struct {
	// binary is the executable Run invokes. Empty selects "systemctl"; tests
	// point it at a fixture to exercise the timeout without depending on the
	// real systemd.
	binary string
}

func (s *defaultSystemCtl) Run(args ...string) error {
	binary := s.binary
	if binary == "" {
		binary = "systemctl"
	}

	timeout := resolveSystemctlTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf(
				"systemctl %s timed out after %s: %s",
				strings.Join(args, " "), timeout, out,
			)
		}
		return fmt.Errorf("%s", out)
	}

	return nil
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
	return linuxSvc.system.Run("is-active", linuxSvc.name) == nil
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
