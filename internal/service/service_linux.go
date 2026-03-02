//go:build linux

package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/RewstApp/agent-smith-go/internal/utils"
)

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
	system systemCtl
}

func (s *defaultServiceManager) Create(params AgentParams) (Service, error) {
	serviceConfig := strings.Builder{}

	fmt.Fprintf(&serviceConfig, "[Unit]\nDescription=%s\n\n", params.Name)
	fmt.Fprintf(
		&serviceConfig,
		"[Service]\nExecStart=%s --org-id %s --config-file %s --log-file %s\nRestart=always\n\n",
		params.AgentExecutablePath,
		params.OrgId,
		params.ConfigFilePath,
		params.LogFilePath,
	)
	fmt.Fprintf(&serviceConfig, "[Install]\nWantedBy=multi-user.target\n")

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
	err := s.system.Run("status", name)
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

func Run(runner Runner) (int, error) {
	// Create a channel to listen for termination signals
	signalReceived := make(chan os.Signal, 1)
	signal.Notify(signalReceived, os.Interrupt, syscall.SIGTERM)

	// Make go routines for the channels
	stop := make(chan struct{})
	ctxStop, cancelStop := context.WithCancel(context.Background())
	defer cancelStop()

	go func() {
		select {
		case <-signalReceived:
			stop <- struct{}{}
		case <-ctxStop.Done():
		}
	}()

	// This channel is unused in linux
	running := make(chan struct{})
	ctxRunning, cancelRunning := context.WithCancel(context.Background())
	defer cancelRunning()

	go func() {
		select {
		case <-running:
		case <-ctxRunning.Done():
		}
	}()

	// Execute the runner
	exitCode := runner.Execute(stop, running)

	return int(exitCode), nil
}
