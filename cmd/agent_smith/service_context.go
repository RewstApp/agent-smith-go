package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/interpreter"
)

type serviceContext struct {
	OrgId      string
	ConfigFile string
	LogFile    string

	Sys    agent.SystemInfoProvider
	Domain agent.DomainInfoProvider

	Executor   interpreter.Executor
	HTTPClient *http.Client

	// PostbackMaxAttempts is the total number of postback attempts (including
	// the initial try) before giving up. Defaults to postbackMaxAttempts.
	PostbackMaxAttempts int
	// PostbackBaseRetryBackoff is the base delay used for exponential backoff
	// between postback attempts. Defaults to postbackBaseRetryBackoff.
	PostbackBaseRetryBackoff time.Duration

	// droppedMessages counts inbound messages the agent could not accept and had
	// to discard. Under normal operation the subscribe callback applies
	// back-pressure instead of dropping, so this only increments when a payload
	// arrives during teardown (see runCycle). It is a cumulative, process-wide
	// counter exposed for observability beyond the per-drop error log.
	droppedMessages atomic.Int64
}

// newServiceFlagSet builds the flag set for service mode, binding flags to the
// provided params. It is shared between argument parsing and usage rendering so
// that the per-flag descriptions stay in a single place.
func newServiceFlagSet(params *serviceContext) *flag.FlagSet {
	fs := flag.NewFlagSet("service", flag.ContinueOnError)
	fs.StringVar(&params.OrgId, "org-id", "", "Organization ID")
	fs.StringVar(&params.ConfigFile, "config-file", "", "Configuration File")
	fs.StringVar(&params.LogFile, "log-file", "", "Log file")
	fs.SetOutput(io.Discard)
	return fs
}

func newServiceContext(
	args []string,
	sys agent.SystemInfoProvider,
	domain agent.DomainInfoProvider,
	executor interpreter.Executor,
) (*serviceContext, error) {
	var params serviceContext

	fs := newServiceFlagSet(&params)

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
	params.Executor = executor
	params.HTTPClient = &http.Client{Timeout: postbackHTTPTimeout}
	params.PostbackMaxAttempts = postbackMaxAttempts
	params.PostbackBaseRetryBackoff = postbackBaseRetryBackoff

	return &params, nil
}
