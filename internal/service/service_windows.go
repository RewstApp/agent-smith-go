//go:build windows

package service

import (
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
