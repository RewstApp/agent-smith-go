package agent

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/process"
)

// ProcessUsage is what the agent process itself costs the endpoint
// (sc-132992), reported in the device twin's fleet-health object so a leak -
// goroutines from a plugin supervisor, descriptors from the bounded writers,
// memory from output buffers - shows as a number climbing in Rewst before a
// customer reports a slow machine. A field whose query failed is zero.
type ProcessUsage struct {
	// RSSBytes is the resident set (the working set on Windows).
	RSSBytes uint64
	// CPUSeconds is user plus system time since the process started.
	CPUSeconds float64
	Goroutines int
	Threads    int32
	// OpenFiles is the descriptor count on Linux and macOS; Handles is the
	// handle count on Windows. The other is always zero.
	OpenFiles int32
	Handles   int32
}

// processQueries are the per-field queries ProcessUsage is sampled from, held
// as fields so a test can substitute one that blocks or fails. A zero value
// selects the real queries against this process.
type processQueries struct {
	memory      func(ctx context.Context) (*process.MemoryInfoStat, error)
	times       func(ctx context.Context) (*cpu.TimesStat, error)
	threads     func(ctx context.Context) (int32, error)
	descriptors func(ctx context.Context) (int32, error)
}

func (q processQueries) orDefault() processQueries {
	self := &process.Process{Pid: int32(os.Getpid())} // #nosec G115 - a pid fits
	if q.memory == nil {
		q.memory = self.MemoryInfoWithContext
	}
	if q.times == nil {
		q.times = self.TimesWithContext
	}
	if q.threads == nil {
		q.threads = self.NumThreadsWithContext
	}
	if q.descriptors == nil {
		q.descriptors = func(ctx context.Context) (int32, error) {
			return countDescriptors(ctx, self)
		}
	}
	return q
}

// SampleProcessUsage reads the agent process's resource usage. Every query
// shares one hostCommandTimeout budget - the health loop takes this sample on
// its own goroutine and must not be held for longer than one bound however
// many queries stall - and each runs abandonably: most of these are syscalls
// that take no context, so a query still running when the budget ends is
// left to finish on its own and its field stays zero. The error joins every
// query that failed, naming it; the usage is always returned with what was
// read.
func SampleProcessUsage(ctx context.Context) (ProcessUsage, error) {
	return sampleProcessUsage(ctx, processQueries{})
}

func sampleProcessUsage(ctx context.Context, q processQueries) (ProcessUsage, error) {
	q = q.orDefault()
	timeout := resolveHostCommandTimeout()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	usage := ProcessUsage{Goroutines: runtime.NumGoroutine()}
	var errs []error
	if mem, err := abandonable(ctx, "resident memory", timeout, q.memory); err != nil {
		errs = append(errs, err)
	} else if mem != nil {
		usage.RSSBytes = mem.RSS
	}
	if times, err := abandonable(ctx, "cpu time", timeout, q.times); err != nil {
		errs = append(errs, err)
	} else if times != nil {
		// Centiseconds: the twin carries a number an operator compares, and
		// the sub-millisecond digits only add noise to every publish.
		usage.CPUSeconds = math.Round((times.User+times.System)*100) / 100
	}
	if threads, err := abandonable(ctx, "thread count", timeout, q.threads); err != nil {
		errs = append(errs, err)
	} else {
		usage.Threads = threads
	}
	if n, err := abandonable(ctx, descriptorName, timeout, q.descriptors); err != nil {
		errs = append(errs, err)
	} else if runtime.GOOS == "windows" {
		usage.Handles = n
	} else {
		usage.OpenFiles = n
	}
	return usage, errors.Join(errs...)
}

// abandonable runs query under ctx and returns when it answers or ctx ends,
// whichever is first; a query that outlives ctx is abandoned (its goroutine
// ends when the query does) and reported with the bound it overran.
func abandonable[T any](
	ctx context.Context,
	what string,
	timeout time.Duration,
	query func(context.Context) (T, error),
) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, fmt.Errorf("%s query not run: %w", what, err)
	}
	type answer struct {
		value T
		err   error
	}
	done := make(chan answer, 1)
	go func() {
		value, err := query(ctx)
		done <- answer{value, err}
	}()
	select {
	case a := <-done:
		if a.err != nil {
			return zero, fmt.Errorf("%s query: %w", what, a.err)
		}
		return a.value, nil
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return zero, fmt.Errorf("%s query timed out after %s: %w", what, timeout, ctx.Err())
		}
		return zero, fmt.Errorf("%s query canceled: %w", what, ctx.Err())
	}
}
