package main

import (
	"bytes"
	"flag"
	"fmt"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
)

type updateContext struct {
	OrgId                string
	Update               bool
	LoggingLevel         string
	UseSyslog            bool
	DisableAgentPostback bool
	NoAutoUpdates        bool

	Sys    agent.SystemInfoProvider
	Domain agent.DomainInfoProvider
}

func newUpdateContext(args []string, sys agent.SystemInfoProvider, domain agent.DomainInfoProvider) (*updateContext, error) {
	var params updateContext

	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.StringVar(&params.OrgId, "org-id", "", "Organization ID")
	fs.BoolVar(&params.Update, "update", false, "Update the agent")
	fs.StringVar(&params.LoggingLevel, "logging-level", string(utils.Default), fmt.Sprintf("Logging level: %s", getAllowedConfigLevelsString(", ")))
	fs.BoolVar(&params.UseSyslog, "syslog", false, "Write log messages to system log")
	fs.BoolVar(&params.DisableAgentPostback, "disable-agent-postback", false, "Disable agent postback")
	fs.BoolVar(&params.NoAutoUpdates, "no-auto-updates", false, "No auto updates")
	fs.SetOutput(bytes.NewBuffer([]byte{}))

	err := fs.Parse(args)
	if err != nil {
		return nil, err
	}

	if params.OrgId == "" {
		return nil, fmt.Errorf("missing org-id")
	}

	if !params.Update {
		return nil, fmt.Errorf("missing update")
	}

	if !allowedLoggingLevels[params.LoggingLevel] {
		return nil, fmt.Errorf("invalid logging-level")
	}

	params.Sys = sys
	params.Domain = domain

	return &params, nil
}
