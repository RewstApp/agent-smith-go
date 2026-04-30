//go:build linux

package service

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/RewstApp/agent-smith-go/internal/utils"
)

func chownR(dir, username string) error {
	u, err := user.Lookup(username)
	if err != nil {
		return fmt.Errorf("user %q not found: %w", username, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return err
	}
	return filepath.Walk(dir, func(path string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(path, uid, gid)
	})
}

type systemCtl interface {
	Run(args ...string) error
	ServiceConfigFilePath(name string) string
}

type defaultSystemCtl struct{}

func (s *defaultSystemCtl) Run(args ...string) error {
	cmd := exec.Command("systemctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
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
	system   systemCtl
	chownDir func(dir, username string) error
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

	if params.ServiceUsername != "" && s.chownDir != nil {
		if err := s.chownDir(filepath.Dir(params.ConfigFilePath), params.ServiceUsername); err != nil {
			return nil, err
		}
		if params.ScriptsDirectory != "" {
			if err := os.MkdirAll(params.ScriptsDirectory, 0o755); err != nil {
				return nil, err
			}
			if err := s.chownDir(params.ScriptsDirectory, params.ServiceUsername); err != nil {
				return nil, err
			}
		}
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
		system:   &defaultSystemCtl{},
		chownDir: chownR,
	}
}
