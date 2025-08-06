//go:build darwin

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

func runLaunchCtl(args ...string) ([]byte, error) {
	cmd := exec.Command("launchctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s", out)
	}

	return out, nil
}

type darwinService struct {
	name string
}

func (svc *darwinService) serviceFilePath() string {
	return filepath.Join("/Library/LaunchDaemons", fmt.Sprintf("%s.plist", svc.name))
}

func (svc *darwinService) Close() error {
	return nil
}

func (svc *darwinService) Start() error {
	_, err := runLaunchCtl("load", svc.serviceFilePath())
	if err != nil {
		return err
	}

	_, err = runLaunchCtl("start", svc.name)
	return err
}

func (svc *darwinService) Stop() error {
	_, err := runLaunchCtl("stop", svc.name)
	if err != nil {
		return err
	}
	_, err = runLaunchCtl("unload", svc.serviceFilePath())
	return err
}

func (svc *darwinService) Delete() error {
	_, err := runLaunchCtl("unload", svc.name)
	if err != nil {
		return err
	}

	// Delete the service configuration file
	return os.Remove(svc.serviceFilePath())
}

func (svc *darwinService) IsActive() bool {
	out, err := runLaunchCtl("list")
	if err != nil {
		return false
	}

	// Find the line that contains the service name
	for line := range strings.SplitSeq(string(out), "\n") {
		if strings.Contains(line, svc.name) {
			return true
		}
	}

	// Service name was not found
	return false
}

func Create(params AgentParams) (Service, error) {
	serviceConfig := strings.Builder{}

	serviceConfig.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	serviceConfig.WriteString("<!DOCTYPE plist PUBLIC \"-//Apple//DTD PLIST 1.0//EN\"\n")
	serviceConfig.WriteString("\"http://www.apple.com/DTDs/PropertyList-1.0.dtd\">\n")

	serviceConfig.WriteString("<plist version=\"1.0\">\n")
	serviceConfig.WriteString("<dict>\n")

	serviceConfig.WriteString("<key>Label</key>\n")
	serviceConfig.WriteString("<string>")
	serviceConfig.WriteString(params.Name)
	serviceConfig.WriteString("</string>\n")

	serviceConfig.WriteString("<key>ProgramArguments</key>\n")
	serviceConfig.WriteString("<array>\n")
	serviceConfig.WriteString("<string>")
	serviceConfig.WriteString(params.AgentExecutablePath)
	serviceConfig.WriteString("</string>\n")
	serviceConfig.WriteString("<string>--org-id</string>\n")
	serviceConfig.WriteString("<string>")
	serviceConfig.WriteString(params.OrgId)
	serviceConfig.WriteString("</string>\n")
	serviceConfig.WriteString("<string>--config-file</string>\n")
	serviceConfig.WriteString("<string>")
	serviceConfig.WriteString(params.OrgId)
	serviceConfig.WriteString("</string>\n")
	serviceConfig.WriteString("<string>--log-file</string>\n")
	serviceConfig.WriteString("<string>")
	serviceConfig.WriteString(params.LogFilePath)
	serviceConfig.WriteString("</string>\n")
	serviceConfig.WriteString("</array>\n")

	serviceConfig.WriteString("<key>RunAtLoad</key>\n")
	serviceConfig.WriteString("<true/>\n")

	serviceConfig.WriteString("<key>KeepAlive</key>\n")
	serviceConfig.WriteString("<true/>\n")

	serviceConfig.WriteString("</dict>\n")
	serviceConfig.WriteString("</plist>\n")

	svc := &darwinService{
		name: params.Name,
	}

	err := os.WriteFile(svc.serviceFilePath(), []byte(serviceConfig.String()), utils.DefaultFileMod)
	if err != nil {
		return nil, err
	}

	return svc, nil
}

func Open(name string) (Service, error) {
	_, err := runLaunchCtl("print", fmt.Sprintf("system/%s", name))
	if err != nil {
		return nil, err
	}

	return &darwinService{
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

	// This channel is unused in darwin
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
