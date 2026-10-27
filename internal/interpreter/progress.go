package interpreter

import (
	"sort"
	"sync"
	"time"
)

// lastOutputLineMax bounds the last output line a heartbeat carries, so a
// script that prints one enormous line cannot bloat the device twin. The
// bounded writer tracks the tail as it is written (see trackLinesLocked).
const lastOutputLineMax = 256

// Progress is a running command's state for the device twin's heartbeat
// (sc-129689): when it started, how much it has written to each stream
// (produced, not kept - a truncated stream keeps counting), and the last line
// of its standard output (standard error when stdout has none).
type Progress struct {
	PostID         string
	StartedAt      time.Time
	StdoutBytes    int64
	StderrBytes    int64
	LastOutputLine string
}

// ProgressReporter is implemented by executors that track the commands they
// are running. The service asserts for it rather than widening Executor, so
// test doubles need not implement it.
type ProgressReporter interface {
	InFlight() []Progress
}

// inFlightCommand is one running command's handles into the executor.
type inFlightCommand struct {
	postID    string
	startedAt time.Time
	stdout    *boundedWriter
	stderr    *boundedWriter
}

// inFlightSet tracks the commands an executor is running. Zero value ready.
type inFlightSet struct {
	mu      sync.Mutex
	running map[*inFlightCommand]struct{}
}

func (s *inFlightSet) add(postID string, stdout, stderr *boundedWriter) *inFlightCommand {
	c := &inFlightCommand{postID: postID, startedAt: time.Now(), stdout: stdout, stderr: stderr}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running == nil {
		s.running = map[*inFlightCommand]struct{}{}
	}
	s.running[c] = struct{}{}
	return c
}

func (s *inFlightSet) remove(c *inFlightCommand) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.running, c)
}

// snapshot reports every running command, oldest first.
func (s *inFlightSet) snapshot() []Progress {
	s.mu.Lock()
	running := make([]*inFlightCommand, 0, len(s.running))
	for c := range s.running {
		running = append(running, c)
	}
	s.mu.Unlock()
	sort.Slice(
		running,
		func(i, j int) bool { return running[i].startedAt.Before(running[j].startedAt) },
	)
	out := make([]Progress, 0, len(running))
	for _, c := range running {
		out = append(out, Progress{
			PostID:         c.postID,
			StartedAt:      c.startedAt,
			StdoutBytes:    c.stdout.Produced(),
			StderrBytes:    c.stderr.Produced(),
			LastOutputLine: lastOutputLine(c.stdout, c.stderr),
		})
	}
	return out
}

// lastOutputLine is the last non-empty line of stdout, or of stderr when stdout
// has none, truncated to lastOutputLineMax bytes.
func lastOutputLine(stdout, stderr *boundedWriter) string {
	if line := stdout.LastLine(); line != "" {
		return line
	}
	return stderr.LastLine()
}

// inFlightOf gathers the running commands of every executor that reports them.
func inFlightOf(executors ...Executor) []Progress {
	var out []Progress
	for _, e := range executors {
		if r, ok := e.(ProgressReporter); ok {
			out = append(out, r.InFlight()...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}
