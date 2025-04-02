//go:build windows

package service

import (
	"context"
	"fmt"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const pollingInterval = 250 * time.Millisecond

type windowsService struct {
	handle *mgr.Service
}

func (winSvc *windowsService) Close() error {
	return winSvc.handle.Close()
}

func (winSvc *windowsService) Start() error {
	return winSvc.handle.Start()
}

func (winSvc *windowsService) Stop() error {
	status, err := winSvc.handle.Control(svc.Stop)
	if err != nil {
		return err
	}

	// Wait for the service to stop by polling the status
	for {
		if status.State == svc.Stopped {
			return nil
		}

		time.Sleep(pollingInterval)

		status, err = winSvc.handle.Query()
		if err != nil {
			return err
		}
	}
}

func (winSvc *windowsService) Delete() error {
	return winSvc.handle.Delete()
}

func (winSvc *windowsService) IsActive() bool {
	status, err := winSvc.handle.Query()
	if err != nil {
		return false
	}

	return status.State == svc.Running
}

func Create(params AgentParams) (Service, error) {
	svcMgr, err := mgr.Connect()
	if err != nil {
		return nil, err
	}
	defer svcMgr.Disconnect()

	svc, err := svcMgr.CreateService(params.Name, params.AgentExecutablePath, mgr.Config{
		StartType:        mgr.StartAutomatic,
		Description:      fmt.Sprintf("Rewst Remote Agent for Org %s", params.OrgId),
		DelayedAutoStart: true,
	}, "--org-id", params.OrgId, "--config-file", params.ConfigFilePath, "--log-file", params.LogFilePath)
	if err != nil {
		return nil, err
	}

	return &windowsService{
		handle: svc,
	}, nil
}

func Open(name string) (Service, error) {
	svcMgr, err := mgr.Connect()
	if err != nil {
		return nil, err
	}
	defer svcMgr.Disconnect()

	svc, err := svcMgr.OpenService(name)
	if err != nil {
		return nil, err
	}

	return &windowsService{
		handle: svc,
	}, nil
}

type windowsRunner struct {
	runner   Runner
	exitCode int
}

func (host *windowsRunner) Execute(args []string, request <-chan svc.ChangeRequest, response chan<- svc.Status) (bool, uint32) {
	response <- svc.Status{State: svc.StartPending}

	// Make the channels
	stop := make(chan struct{})
	running := make(chan struct{})

	// Make go routines for the channels
	ctxStop, cancelStop := context.WithCancel(context.Background())
	defer cancelStop()
	go func() {
		for {
			select {
			case change := <-request:
				switch change.Cmd {
				case svc.Stop, svc.Shutdown:
					stop <- struct{}{}
					return
				}
			case <-ctxStop.Done():
				// Stop this routine
				return
			}
		}
	}()

	ctxRunning, cancelRunning := context.WithCancel(context.Background())
	defer cancelRunning()
	go func() {
		select {
		case <-running:
			response <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
		case <-ctxRunning.Done():
			// Stop this routine
			return
		}
	}()

	// Execute the runner
	host.exitCode = host.runner.Execute(stop, running)
	response <- svc.Status{State: svc.Stopped}

	// Return the proper response
	return host.exitCode == 0, uint32(host.exitCode)
}

func Run(runner Runner) (int, error) {
	// Check if this is running as a service
	isWinSvc, err := svc.IsWindowsService()
	if err != nil {
		return 1, err
	}

	if !isWinSvc {
		return 1, fmt.Errorf("executable should be run as a service")
	}

	// Start the windows service
	host := &windowsRunner{
		runner: runner,
	}
	err = svc.Run(runner.Name(), host)
	if err != nil {
		return host.exitCode, err
	}

	return host.exitCode, nil
}
