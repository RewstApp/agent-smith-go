package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/utils"
)

type updateContext struct {
	OrgId                string
	Update               bool
	LoggingLevel         string
	UseSyslog            bool
	DisableAgentPostback bool
	NoAutoUpdates        bool
	GithubToken          string
	MqttQos              int
	ServiceUsername      string
	ServicePassword      string

	Sys    agent.SystemInfoProvider
	Domain agent.DomainInfoProvider

	ServiceManager service.ServiceManager
	FS             utils.FileSystem
}

// newUpdateFlagSet builds the flag set for update mode, binding flags to the
// provided params. It is shared between argument parsing and usage rendering so
// that the per-flag descriptions stay in a single place.
func newUpdateFlagSet(params *updateContext) *flag.FlagSet {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.StringVar(&params.OrgId, "org-id", "", "Organization ID")
	fs.BoolVar(&params.Update, "update", false, "Update the agent")
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
	fs.IntVar(&params.MqttQos, "mqtt-qos", -1, "MQTT subscription QoS level (0 or 1)")
	fs.StringVar(
		&params.ServiceUsername,
		"service-username",
		"",
		"User account the service should run as (re-registers the service when set)",
	)
	fs.StringVar(
		&params.ServicePassword,
		"service-password",
		"",
		"Password for --service-username (Windows only; not persisted to disk)",
	)
	fs.SetOutput(io.Discard)
	return fs
}

func newUpdateContext(
	args []string,
	sys agent.SystemInfoProvider,
	domain agent.DomainInfoProvider,
	svcMgr service.ServiceManager,
	fsys utils.FileSystem,
) (*updateContext, error) {
	var params updateContext

	fs := newUpdateFlagSet(&params)

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

	if params.MqttQos != -1 && (params.MqttQos < 0 || params.MqttQos > 1) {
		return nil, fmt.Errorf("invalid mqtt-qos: must be 0 or 1")
	}

	if params.ServicePassword != "" && params.ServiceUsername == "" {
		return nil, fmt.Errorf("service-password requires service-username")
	}

	params.Sys = sys
	params.Domain = domain
	params.ServiceManager = svcMgr
	params.FS = fsys

	return &params, nil
}
