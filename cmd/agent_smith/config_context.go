package main

import (
	"bytes"
	"flag"
	"fmt"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/utils"
)

type configContext struct {
	OrgId                string
	ConfigUrl            string
	ConfigSecret         string
	LoggingLevel         string
	UseSyslog            bool
	DisableAgentPostback bool
	NoAutoUpdates        bool
	GithubToken          string

	Sys    agent.SystemInfoProvider
	Domain agent.DomainInfoProvider

	FS             utils.FileSystem
	ServiceManager service.ServiceManager
}

func newConfigContext(
	args []string,
	sys agent.SystemInfoProvider,
	domain agent.DomainInfoProvider,
	fsys utils.FileSystem,
	svcMgr service.ServiceManager,
) (*configContext, error) {
	var params configContext

	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.StringVar(&params.OrgId, "org-id", "", "Organization ID")
	fs.StringVar(&params.ConfigUrl, "config-url", "", "Configuration URL")
	fs.StringVar(&params.ConfigSecret, "config-secret", "", "Configuration Secret")
	fs.StringVar(
		&params.LoggingLevel,
		"logging-level",
		string(utils.Default),
		fmt.Sprintf("Logging level: %s", getAllowedConfigLevelsString(", ")),
	)
	fs.BoolVar(&params.UseSyslog, "syslog", false, "Write log messages to system log")
	fs.BoolVar(
		&params.DisableAgentPostback,
		"disable-agent-postback",
		false,
		"Disable agent postback",
	)
	fs.BoolVar(&params.NoAutoUpdates, "no-auto-updates", false, "No auto updates")
	fs.StringVar(&params.GithubToken, "github-token", "", "GitHub token for update checks")
	fs.SetOutput(bytes.NewBuffer([]byte{}))

	err := fs.Parse(args)
	if err != nil {
		return nil, err
	}

	if params.OrgId == "" {
		return nil, fmt.Errorf("missing org-id")
	}

	if params.ConfigUrl == "" {
		return nil, fmt.Errorf("missing config-url")
	}

	if params.ConfigSecret == "" {
		return nil, fmt.Errorf("missing config-secret")
	}

	if !allowedLoggingLevels[params.LoggingLevel] {
		return nil, fmt.Errorf("invalid logging-level")
	}

	params.Sys = sys
	params.Domain = domain
	params.FS = fsys
	params.ServiceManager = svcMgr

	return &params, nil
}
