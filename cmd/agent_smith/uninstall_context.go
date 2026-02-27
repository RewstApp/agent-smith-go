package main

import (
	"bytes"
	"flag"
	"fmt"

	"github.com/RewstApp/agent-smith-go/internal/service"
)

type uninstallContext struct {
	OrgId     string
	Uninstall bool

	ServiceManager service.ServiceManager
}

func newUninstallContext(args []string, svcMgr service.ServiceManager) (*uninstallContext, error) {
	var params uninstallContext

	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.StringVar(&params.OrgId, "org-id", "", "Organization ID")
	fs.BoolVar(&params.Uninstall, "uninstall", false, "Uninstall the agent")
	fs.SetOutput(bytes.NewBuffer([]byte{}))

	err := fs.Parse(args)
	if err != nil {
		return nil, err
	}

	if params.OrgId == "" {
		return nil, fmt.Errorf("missing org-id")
	}

	if !params.Uninstall {
		return nil, fmt.Errorf("missing uninstall")
	}

	params.ServiceManager = svcMgr

	return &params, nil
}
