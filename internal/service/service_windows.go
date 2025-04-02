//go:build windows

package service

import (
	"fmt"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

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
	// TODO: Add stop routine
	_, err := winSvc.handle.Control(svc.Stop)
	return err
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
