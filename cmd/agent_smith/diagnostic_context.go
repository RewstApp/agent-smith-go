package main

import (
	"flag"
	"fmt"
	"io"

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

// newDiagnosticFlagSet builds the flag set for diagnostic mode, binding flags
// to the provided params. It is shared between argument parsing and usage
// rendering so that the per-flag descriptions stay in a single place.
func newDiagnosticFlagSet(params *diagnosticContext) *flag.FlagSet {
	fs := flag.NewFlagSet("diagnostic", flag.ContinueOnError)
	fs.StringVar(&params.OrgId, "org-id", "", "Organization ID")
	fs.BoolVar(&params.Diagnostic, "diagnostic", false, "Run diagnostic mode")
	fs.SetOutput(io.Discard)
	return fs
}

func newDiagnosticContext(
	args []string,
	sys agent.SystemInfoProvider,
	domain agent.DomainInfoProvider,
	svcMgr service.ServiceManager,
	fsys utils.FileSystem,
) (*diagnosticContext, error) {
	var params diagnosticContext

	fs := newDiagnosticFlagSet(&params)

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
