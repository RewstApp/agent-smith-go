package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/mem"
)

// The system-info queries run under the agent's own hostCommandTimeout on
// every platform (sc-129668). A query that outlives the bound fails with an
// error naming the query and the bound; one that answers within it succeeds;
// the caller's cancellation is honoured and named too.

func withHostCommandTimeout(t *testing.T, override string) {
	t.Helper()
	saved := hostCommandTimeoutOverrideStr
	hostCommandTimeoutOverrideStr = override
	t.Cleanup(func() { hostCommandTimeoutOverrideStr = saved })
}

// blockUntilDone is a query that only returns when its context ends - the
// shape of a wedged WMI repository.
func blockUntilDone[T any](ctx context.Context) (T, error) {
	var zero T
	<-ctx.Done()
	return zero, ctx.Err()
}

func TestBoundedQuery_NamesTheQueryAndTheBoundOnTimeout(t *testing.T) {
	withHostCommandTimeout(t, "50ms")
	started := time.Now()
	_, err := hostPlatform(
		context.Background(),
		systemQueries{hostInfo: blockUntilDone[*host.InfoStat]},
	)
	if err == nil {
		t.Fatal("a query that never answers succeeded")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error does not wrap the deadline: %v", err)
	}
	for _, want := range []string{"host platform query timed out after 50ms"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("the bound did not end the query: waited %s", elapsed)
	}
}

func TestBoundedQuery_HonoursTheCallerCancellation(t *testing.T) {
	withHostCommandTimeout(t, "10s")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := cpuModelName(ctx, systemQueries{cpuInfo: blockUntilDone[[]cpu.InfoStat]})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected the caller's cancellation, got %v", err)
	}
	if !strings.Contains(err.Error(), "cpu model query canceled") {
		t.Errorf("error %q does not name the query", err)
	}
}

func TestBoundedQuery_AnswersWithinTheBound(t *testing.T) {
	withHostCommandTimeout(t, "10s")
	q := systemQueries{
		hostInfo: func(context.Context) (*host.InfoStat, error) {
			return &host.InfoStat{Platform: "test-os"}, nil
		},
		cpuInfo: func(context.Context) ([]cpu.InfoStat, error) {
			return []cpu.InfoStat{{ModelName: "  Test CPU  "}}, nil
		},
		memory: func(context.Context) (*mem.VirtualMemoryStat, error) {
			return &mem.VirtualMemoryStat{Total: 4096}, nil
		},
	}
	ctx := context.Background()
	if got, err := hostPlatform(ctx, q); err != nil || got != "test-os" {
		t.Errorf("hostPlatform = %q, %v", got, err)
	}
	if got, err := cpuModelName(ctx, q); err != nil || got != "Test CPU" {
		t.Errorf("cpuModelName = %q, %v", got, err)
	}
	if got, err := totalMemoryBytes(ctx, q); err != nil || got != 4096 {
		t.Errorf("totalMemoryBytes = %d, %v", got, err)
	}
}

func TestBoundedQuery_PassesADeadlineToTheQuery(t *testing.T) {
	// gopsutil only applies its own 3 s default when the context carries no
	// deadline; the bound must therefore reach the query as a deadline.
	withHostCommandTimeout(t, "10s")
	var sawDeadline bool
	q := systemQueries{memory: func(ctx context.Context) (*mem.VirtualMemoryStat, error) {
		_, sawDeadline = ctx.Deadline()
		return &mem.VirtualMemoryStat{Total: 1}, nil
	}}
	if _, err := totalMemoryBytes(context.Background(), q); err != nil {
		t.Fatal(err)
	}
	if !sawDeadline {
		t.Error("the query ran under a context with no deadline")
	}
}

func TestBoundedQuery_ReportsAnEmptyCPUList(t *testing.T) {
	q := systemQueries{cpuInfo: func(context.Context) ([]cpu.InfoStat, error) { return nil, nil }}
	if _, err := cpuModelName(context.Background(), q); err == nil {
		t.Error("an empty processor list was accepted")
	}
}

func TestSystemQueries_ZeroValueSelectsGopsutil(t *testing.T) {
	q := systemQueries{}.orDefault()
	if q.hostInfo == nil || q.cpuInfo == nil || q.memory == nil {
		t.Error("orDefault left a query unset")
	}
}

func TestNewSystemInfoProvider_AnswersUnderTheBound(t *testing.T) {
	ctx := context.Background()
	sys := NewSystemInfoProvider()
	if _, err := sys.HostPlatform(ctx); err != nil {
		t.Errorf("HostPlatform: %v", err)
	}
	if _, err := sys.CPUModelName(ctx); err != nil {
		t.Errorf("CPUModelName: %v", err)
	}
	if got, err := sys.TotalMemoryBytes(ctx); err != nil || got == 0 {
		t.Errorf("TotalMemoryBytes = %d, %v", got, err)
	}
}
