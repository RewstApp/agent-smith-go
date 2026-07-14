package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/utils"
)

const configHTTPTimeout = 5 * time.Minute

// tuningFlagUnset is the sentinel default for the optional integer tuning flags
// (e.g. --worker-count). It mirrors the --mqtt-qos sentinel pattern: the value
// is only applied to the configuration when the operator explicitly provides a
// flag, otherwise the agent falls back to its documented default.
const tuningFlagUnset = -1

// tuningFlags groups the optional integer tuning parameters shared by config and
// update modes. Each field defaults to tuningFlagUnset so an omitted flag leaves
// the corresponding configuration field alone.
type tuningFlags struct {
	MqttConnectTimeoutSeconds       int
	WorkerCount                     int
	MessageQueueSize                int
	PostbackMaxAttempts             int
	PostbackBaseRetryBackoffSeconds int
	// provided records which tuning flag names the operator explicitly set. It is
	// populated from flag.FlagSet.Visit after parsing so validation can flag an
	// explicitly-provided non-positive value (e.g. --worker-count -1) even when it
	// collides with the unset sentinel.
	provided map[string]bool
}

// tuningFlagNames lists the flag names bound by bindTuningFlags, in the order
// they appear in usage output.
var tuningFlagNames = []string{
	"mqtt-connect-timeout-seconds",
	"worker-count",
	"message-queue-size",
	"postback-max-attempts",
	"postback-base-retry-backoff-seconds",
}

// captureProvided records which tuning flags were explicitly set on fs so that
// validation can distinguish an omitted flag from one that was given a value
// (including an invalid negative value that matches the unset sentinel).
func (t *tuningFlags) captureProvided(fs *flag.FlagSet) {
	names := map[string]bool{}
	for _, name := range tuningFlagNames {
		names[name] = true
	}
	t.provided = map[string]bool{}
	fs.Visit(func(f *flag.Flag) {
		if names[f.Name] {
			t.provided[f.Name] = true
		}
	})
}

// bindTuningFlags registers the shared tuning flags on fs so config and update
// modes expose an identical set of options with identical descriptions.
func bindTuningFlags(fs *flag.FlagSet, t *tuningFlags) {
	fs.IntVar(
		&t.MqttConnectTimeoutSeconds,
		"mqtt-connect-timeout-seconds",
		tuningFlagUnset,
		"MQTT connect timeout in seconds (positive integer)",
	)
	fs.IntVar(
		&t.WorkerCount,
		"worker-count",
		tuningFlagUnset,
		"Number of concurrent command-execution workers (positive integer)",
	)
	fs.IntVar(
		&t.MessageQueueSize,
		"message-queue-size",
		tuningFlagUnset,
		"Capacity of the inbound message queue (positive integer)",
	)
	fs.IntVar(
		&t.PostbackMaxAttempts,
		"postback-max-attempts",
		tuningFlagUnset,
		"Total postback attempts before spooling to disk (positive integer)",
	)
	fs.IntVar(
		&t.PostbackBaseRetryBackoffSeconds,
		"postback-base-retry-backoff-seconds",
		tuningFlagUnset,
		"Base exponential-backoff delay between postback attempts in seconds (positive integer)",
	)
}

// validate rejects any tuning flag that was explicitly provided with a
// non-positive value. Flags left unset are ignored so the agent falls back to
// its documented default.
func (t tuningFlags) validate() error {
	checks := []struct {
		name  string
		value int
	}{
		{"mqtt-connect-timeout-seconds", t.MqttConnectTimeoutSeconds},
		{"worker-count", t.WorkerCount},
		{"message-queue-size", t.MessageQueueSize},
		{"postback-max-attempts", t.PostbackMaxAttempts},
		{"postback-base-retry-backoff-seconds", t.PostbackBaseRetryBackoffSeconds},
	}
	for _, c := range checks {
		if t.provided[c.name] && c.value <= 0 {
			return fmt.Errorf("invalid %s: must be a positive integer", c.name)
		}
	}
	return nil
}

// tuningPtr returns a pointer to value when the flag was explicitly provided, or
// nil when it was left at the unset sentinel (fall back to default).
func tuningPtr(value int) *int {
	if value == tuningFlagUnset {
		return nil
	}
	v := value
	return &v
}

type configContext struct {
	OrgId                string
	ConfigUrl            string
	ConfigSecret         string
	LoggingLevel         string
	UseSyslog            bool
	DisableAgentPostback bool
	NoAutoUpdates        bool
	GithubToken          string
	MqttQos              int
	ServiceUsername      string
	ServicePassword      string
	Tuning               tuningFlags

	Sys    agent.SystemInfoProvider
	Domain agent.DomainInfoProvider

	FS             utils.FileSystem
	ServiceManager service.ServiceManager
	HTTPClient     *http.Client
}

// newConfigFlagSet builds the flag set for config mode, binding flags to the
// provided params. It is shared between argument parsing and usage rendering so
// that the per-flag descriptions stay in a single place.
func newConfigFlagSet(params *configContext) *flag.FlagSet {
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
	fs.IntVar(&params.MqttQos, "mqtt-qos", -1, "MQTT subscription QoS level (0 or 1)")
	bindTuningFlags(fs, &params.Tuning)
	fs.StringVar(
		&params.ServiceUsername,
		"service-username",
		"",
		"User account the service should run as (e.g. DOMAIN\\svc_rewst on Windows, rewst on Linux/macOS)",
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

func newConfigContext(
	args []string,
	sys agent.SystemInfoProvider,
	domain agent.DomainInfoProvider,
	fsys utils.FileSystem,
	svcMgr service.ServiceManager,
) (*configContext, error) {
	var params configContext

	fs := newConfigFlagSet(&params)

	err := fs.Parse(args)
	if err != nil {
		return nil, err
	}

	params.Tuning.captureProvided(fs)

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

	if params.MqttQos != -1 && (params.MqttQos < 0 || params.MqttQos > 1) {
		return nil, fmt.Errorf("invalid mqtt-qos: must be 0 or 1")
	}

	if err := params.Tuning.validate(); err != nil {
		return nil, err
	}

	if params.ServicePassword != "" && params.ServiceUsername == "" {
		return nil, fmt.Errorf("service-password requires service-username")
	}

	params.Sys = sys
	params.Domain = domain
	params.FS = fsys
	params.ServiceManager = svcMgr
	params.HTTPClient = &http.Client{Timeout: configHTTPTimeout}

	return &params, nil
}
