package main

import (
	"bytes"
	"flag"
	"fmt"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/utils"
)

type diagnosticContext struct {
	OrgId      string
	Diagnostic bool

	Sys    agent.SystemInfoProvider
	Domain agent.DomainInfoProvider

	ServiceManager service.ServiceManager
	FS             utils.FileSystem
}

func newDiagnosticContext(
	args []string,
	sys agent.SystemInfoProvider,
	domain agent.DomainInfoProvider,
	svcMgr service.ServiceManager,
	fsys utils.FileSystem,
) (*diagnosticContext, error) {
	var params diagnosticContext

	fs := flag.NewFlagSet("diagnostic", flag.ContinueOnError)
	fs.StringVar(&params.OrgId, "org-id", "", "Organization ID")
	fs.BoolVar(&params.Diagnostic, "diagnostic", false, "Run diagnostic mode")
	fs.SetOutput(bytes.NewBuffer([]byte{}))

	err := fs.Parse(args)
	if err != nil {
		return nil, err
	}

	if !params.Diagnostic {
		return nil, fmt.Errorf("missing diagnostic")
	}

	params.Sys = sys
	params.Domain = domain
	params.ServiceManager = svcMgr
	params.FS = fsys

	return &params, nil
}
