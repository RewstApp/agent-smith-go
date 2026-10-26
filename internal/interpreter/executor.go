package interpreter

import (
	"context"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/hashicorp/go-hclog"
)

type Executor interface {
	Execute(
		ctx context.Context,
		message *Message,
		device agent.Device,
		logger hclog.Logger,
		sys agent.SystemInfoProvider,
		domain agent.DomainInfoProvider,
	) []byte
	AlwaysPostback() bool
}

type BuildExecuteCommandArgsFunc = func(command string) []string

type BuildExecuteFileArgsFunc = func(path string) []string

// ExecutionStats are the command outcomes an executor has produced since
// process start, for the device twin's fleet-health object (sc-129687).
type ExecutionStats struct {
	// Completed counts commands that ran to their own end, whatever their exit
	// code; TimedOut counts those killed at the per-command deadline.
	Completed int64
	TimedOut  int64
}

// StatsReporter is implemented by executors that count their outcomes. The
// service asserts for it rather than widening Executor, so test doubles need
// not implement it.
type StatsReporter interface {
	ExecutionStats() ExecutionStats
}

// executionStatsOf sums the outcomes of every executor that reports them.
func executionStatsOf(executors ...Executor) ExecutionStats {
	var total ExecutionStats
	for _, e := range executors {
		if r, ok := e.(StatsReporter); ok {
			s := r.ExecutionStats()
			total.Completed += s.Completed
			total.TimedOut += s.TimedOut
		}
	}
	return total
}
