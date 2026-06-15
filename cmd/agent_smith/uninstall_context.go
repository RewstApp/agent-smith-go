package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/utils"
)

type uninstallContext struct {
	OrgId     string
	Uninstall bool

	ServiceManager service.ServiceManager
	FS             utils.FileSystem
}

// newUninstallFlagSet builds the flag set for uninstall mode, binding flags to
// the provided params. It is shared between argument parsing and usage
// rendering so that the per-flag descriptions stay in a single place.
func newUninstallFlagSet(params *uninstallContext) *flag.FlagSet {
	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.StringVar(&params.OrgId, "org-id", "", "Organization ID")
	fs.BoolVar(&params.Uninstall, "uninstall", false, "Uninstall the agent")
	fs.SetOutput(io.Discard)
	return fs
}

func newUninstallContext(
	args []string,
	svcMgr service.ServiceManager,
	fsys utils.FileSystem,
) (*uninstallContext, error) {
	var params uninstallContext

	fs := newUninstallFlagSet(&params)

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
	params.FS = fsys

	return &params, nil
}
