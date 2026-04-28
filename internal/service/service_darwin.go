//go:build darwin

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

type launchCtl interface {
	Run(args ...string) ([]byte, error)
	PlistFilePath(name string) string
}

type defaultLaunchCtl struct{}

func (d *defaultLaunchCtl) Run(args ...string) ([]byte, error) {
	cmd := exec.Command("launchctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
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
	system   launchCtl
	chownDir func(dir, username string) error
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
		fmt.Fprintf(&serviceConfig, "<key>UserName</key>\n<string>%s</string>\n", params.ServiceUsername)
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

	if params.ServiceUsername != "" && s.chownDir != nil {
		if err := s.chownDir(filepath.Dir(params.ConfigFilePath), params.ServiceUsername); err != nil {
			return nil, err
		}
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
		system:   &defaultLaunchCtl{},
		chownDir: chownR,
	}
}
