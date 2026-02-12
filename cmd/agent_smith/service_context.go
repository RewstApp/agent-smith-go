package main

import (
	"bytes"
	"flag"
	"fmt"

	"github.com/RewstApp/agent-smith-go/internal/agent"
)

type serviceContext struct {
	OrgId      string
	ConfigFile string
	LogFile    string

	Sys    agent.SystemInfoProvider
	Domain agent.DomainInfoProvider
}

func newServiceContext(args []string, sys agent.SystemInfoProvider, domain agent.DomainInfoProvider) (*serviceContext, error) {
	var params serviceContext

	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.StringVar(&params.OrgId, "org-id", "", "Organization ID")
	fs.StringVar(&params.ConfigFile, "config-file", "", "Configuration File")
	fs.StringVar(&params.LogFile, "log-file", "", "Log file")
	fs.SetOutput(bytes.NewBuffer([]byte{}))

	err := fs.Parse(args)
	if err != nil {
		return nil, err
	}

	if params.OrgId == "" {
		return nil, fmt.Errorf("missing org-id")
	}

	if params.ConfigFile == "" {
		return nil, fmt.Errorf("missing config-file")
	}

	if params.LogFile == "" {
		return nil, fmt.Errorf("missing log-file")
	}

	params.Sys = sys
	params.Domain = domain

	return &params, nil
}
