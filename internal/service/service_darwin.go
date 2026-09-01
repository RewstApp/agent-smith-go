//go:build darwin

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

// launchctlTimeout bounds every launchctl invocation the agent shells out to
// (load, start, stop, unload, print). These are one-shot commands, not the
// polled wait Windows' Stop() performs, but launchd can still wedge - a stuck
// daemon, a corrupted launchd state, a known real-world failure mode - and an
// unbounded exec.Command would then hang install, update, uninstall and
// config indefinitely: the same failure class already fixed for the Windows
// service Stop() wait. Five minutes matches serviceStopTimeout: generous
// enough to never cut off a legitimate stop, short enough that a wedged
// launchctl call fails with an actionable error in the same run rather than
// hanging forever.
const launchctlTimeout = 5 * time.Minute

// launchctlTimeoutOverrideStr is overridable via -ldflags for integration
// testing, the same mechanism stopTimeoutOverrideStr uses for the Windows
// service stop wait. Empty in production builds.
var launchctlTimeoutOverrideStr = ""

func resolveLaunchctlTimeout() time.Duration {
	if launchctlTimeoutOverrideStr != "" {
		if d, err := time.ParseDuration(launchctlTimeoutOverrideStr); err == nil && d > 0 {
			return d
		}
	}
	return launchctlTimeout
}

type launchCtl interface {
	Run(args ...string) ([]byte, error)
	PlistFilePath(name string) string
}

type defaultLaunchCtl struct {
	// binary is the executable Run invokes. Empty selects "launchctl"; tests
	// point it at a fixture to exercise the timeout without depending on the
	// real launchd.
	binary string
}

func (d *defaultLaunchCtl) Run(args ...string) ([]byte, error) {
	binary := d.binary
	if binary == "" {
		binary = "launchctl"
	}

	timeout := resolveLaunchctlTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf(
				"launchctl %s timed out after %s: %s",
				strings.Join(args, " "), timeout, out,
			)
		}
		return nil, fmt.Errorf("%s", out)
	}

	return out, nil
}

func (d *defaultLaunchCtl) PlistFilePath(name string) string {
	return filepath.Join("/Library/LaunchDaemons", fmt.Sprintf("%s.plist", name))
}

type darwinService struct {
	name   string
	system launchCtl
}

func (svc *darwinService) serviceFilePath() string {
	return svc.system.PlistFilePath(svc.name)
}

func (svc *darwinService) Close() error {
	return nil
}

func (svc *darwinService) Start() error {
	_, err := svc.system.Run("load", svc.serviceFilePath())
	if err != nil {
		return err
	}

	_, err = svc.system.Run("start", svc.name)
	return err
}

func (svc *darwinService) Stop() error {
	_, err := svc.system.Run("stop", svc.name)
	if err != nil {
		return err
	}

	_, err = svc.system.Run("unload", svc.serviceFilePath())
	return err
}

func (svc *darwinService) Delete() error {
	_, err := svc.system.Run("unload", svc.name)
	if err != nil {
		return err
	}

	// Delete the service configuration file
	return os.Remove(svc.serviceFilePath())
}

func (svc *darwinService) IsActive() bool {
	out, err := svc.system.Run("print", fmt.Sprintf("system/%s", svc.name))
	if err != nil {
		return false
	}

	// Find the line that contains state name
	for line := range strings.SplitSeq(string(out), "\n") {
		pair := strings.Split(strings.TrimSpace(line), "=")
		if len(pair) != 2 {
			continue
		}

		name := strings.TrimSpace(pair[0])
		if name != "state" {
			continue
		}

		value := strings.TrimSpace(pair[1])
		if value == "running" {
			return true
		}
	}

	// State parameter is not found, assume service is not active
	return false
}

type defaultServiceManager struct {
	system launchCtl
}

func (s *defaultServiceManager) Create(params AgentParams) (Service, error) {
	serviceConfig := strings.Builder{}

	fmt.Fprintf(
		&serviceConfig,
		"<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\"\n\"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n",
	)
	fmt.Fprintf(&serviceConfig, "<plist version=\"1.0\">\n<dict>\n")
	fmt.Fprintf(&serviceConfig, "<key>Label</key>\n<string>%s</string>\n", params.Name)
	fmt.Fprintf(
		&serviceConfig,
		"<key>ProgramArguments</key>\n<array>\n<string>%s</string>\n<string>--org-id</string>\n<string>%s</string>\n<string>--config-file</string>\n<string>%s</string>\n<string>--log-file</string>\n<string>%s</string>\n</array>\n",
		params.AgentExecutablePath,
		params.OrgId,
		params.ConfigFilePath,
		params.LogFilePath,
	)
	if params.ServiceUsername != "" {
		fmt.Fprintf(
			&serviceConfig,
			"<key>UserName</key>\n<string>%s</string>\n",
			params.ServiceUsername,
		)
	}
	fmt.Fprintf(&serviceConfig, "<key>RunAtLoad</key>\n<false/>\n")
	fmt.Fprintf(
		&serviceConfig,
		"<key>KeepAlive</key>\n<dict>\n<key>SuccessfulExit</key>\n<false/>\n</dict>\n",
	)
	fmt.Fprintf(
		&serviceConfig,
		"<key>EnvironmentVariables</key>\n<dict>\n<key>PATH</key>\n<string>/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>\n</dict>\n",
	)
	fmt.Fprintf(&serviceConfig, "</dict>\n</plist>\n")

	svc := &darwinService{name: params.Name, system: s.system}
	err := os.WriteFile(svc.serviceFilePath(), []byte(serviceConfig.String()), utils.DefaultFileMod)
	if err != nil {
		return nil, err
	}

	return svc, nil
}

func (s *defaultServiceManager) Open(name string) (Service, error) {
	_, err := s.system.Run("print", fmt.Sprintf("system/%s", name))
	if err != nil {
		return nil, err
	}

	return &darwinService{
		name:   name,
		system: s.system,
	}, nil
}

func NewServiceManager() ServiceManager {
	return &defaultServiceManager{
		system: &defaultLaunchCtl{},
	}
}
