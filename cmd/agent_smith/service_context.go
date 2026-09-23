package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"sync"
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

	// spool is the bounded on-disk fallback for command results whose postback
	// exhausted its in-line retry budget. It survives transient engine outages so
	// results are re-attempted on a later cycle rather than dropped. It may be nil
	// (e.g. in unit tests that exercise postback logic in isolation), in which
	// case exhausted results are surfaced via log and plugin notification only.
	spool *postbackSpool

	// journal is the durable record of commands accepted from the broker but not
	// yet finished. A message is written here before its MQTT acknowledgement is
	// sent, so an ack means "durably accepted" rather than "buffered in memory",
	// and a process that dies with commands queued or executing replays them on
	// the next start (never-started ones execute; started ones are reported as
	// interrupted). See commandJournal. nil disables journaling, which is what
	// most unit tests want.
	journal *commandJournal

	// workers tracks every command worker started by any connection cycle in
	// this process. Workers outlive the cycle that started them - a cycle
	// ending for a SAS renewal or a lost connection must not kill the commands
	// it is running (sc-118039) - so the service stop path waits on this group
	// rather than on anything cycle-scoped. See startWorkers / stopWorkers.
	workers sync.WaitGroup

	// owned holds the journal keys this process has accepted: queued, running,
	// or posting back. The pre-connect replay snapshot skips them - they are
	// live in this process, not leftovers from a previous one - which is what
	// lets an outgoing cycle's workers keep running while the next cycle
	// replays only what a dead process left behind.
	owned sync.Map

	// droppedMessages counts inbound messages the agent could not accept and had
	// to discard. Under normal operation the subscribe callback applies
	// back-pressure instead of dropping, so this only increments when a payload
	// arrives during teardown (see runCycle). It is a cumulative, process-wide
	// counter exposed for observability beyond the per-drop error log.
	droppedMessages atomic.Int64

	// journalDegraded is true while the command journal is failing writes, so
	// the failure and the eventual recovery are each reported exactly once.
	journalDegraded atomic.Bool
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

	if !isValidOrgId(params.OrgId) {
		return nil, fmt.Errorf("invalid org-id")
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
