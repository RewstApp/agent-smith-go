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

func runSystemCtl(args ...string) error {
	cmd := exec.Command("systemctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s", out)
	}

	return nil
}

type linuxService struct {
	name string
}

func (linuxSvc *linuxService) Close() error {
	return nil
}

func (linuxSvc *linuxService) Start() error {
	return runSystemCtl("start", linuxSvc.name)
}

func (linuxSvc *linuxService) Stop() error {
	return runSystemCtl("stop", linuxSvc.name)
}

func (linuxSvc *linuxService) Delete() error {
	err := runSystemCtl("disable", linuxSvc.name)
	if err != nil {
		return err
	}

	// Delete the service configuration file
	serviceConfigFilePath := filepath.Join("/etc/systemd/system", fmt.Sprintf("%s.service", linuxSvc.name))
	return os.Remove(serviceConfigFilePath)
}

func (linuxSvc *linuxService) IsActive() bool {
	return runSystemCtl("is-active", linuxSvc.name) == nil
}

func Create(params AgentParams) (Service, error) {
	serviceConfig := strings.Builder{}

	serviceConfig.WriteString("[Unit]\n")
	serviceConfig.WriteString(fmt.Sprintf("Description=%s\n", params.Name))
	serviceConfig.WriteString("\n")

	serviceConfig.WriteString("[Service]\n")
	serviceConfig.WriteString(fmt.Sprintf("ExecStart=%s --org-id %s --config-file %s --log-file %s\n",
		params.AgentExecutablePath, params.OrgId, params.ConfigFilePath, params.LogFilePath))
	serviceConfig.WriteString("Restart=always\n")
	serviceConfig.WriteString("\n")

	serviceConfig.WriteString("[Install]\n")
	serviceConfig.WriteString("WantedBy=multi-user.target\n")

	serviceConfigFilePath := filepath.Join("/etc/systemd/system", fmt.Sprintf("%s.service", params.Name))
	err := os.WriteFile(serviceConfigFilePath, []byte(serviceConfig.String()), utils.DefaultFileMod)
	if err != nil {
		return nil, err
	}

	err = runSystemCtl("daemon-reload")
	if err != nil {
		return nil, err
	}

	err = runSystemCtl("enable", params.Name)
	if err != nil {
		return nil, err
	}

	return &linuxService{
		name: params.Name,
	}, nil
}

func Open(name string) (Service, error) {
	return &linuxService{
		name: name,
	}, nil
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

	return exitCode, nil
}
