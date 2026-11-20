package agent

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/process"
)

// The real queries against the test process (sc-132992): every field the
// platform supports is non-zero, and only the platform's own descriptor
// field is set.
func TestSampleProcessUsage_ReadsThisProcess(t *testing.T) {
	usage, err := SampleProcessUsage(context.Background())
	if err != nil {
		t.Fatalf("sampling the test process failed: %v", err)
	}
	if usage.RSSBytes < 1<<20 {
		t.Errorf("rss %d B, want at least 1 MiB for a running Go test binary", usage.RSSBytes)
	}
	if usage.Goroutines < 1 || usage.Threads < 1 {
		t.Errorf("goroutines %d threads %d, want both positive", usage.Goroutines, usage.Threads)
	}
	if usage.CPUSeconds < 0 {
		t.Errorf("cpu seconds %v", usage.CPUSeconds)
	}
	if runtime.GOOS == "windows" {
		if usage.Handles < 1 || usage.OpenFiles != 0 {
			t.Errorf("windows: handles %d open_files %d", usage.Handles, usage.OpenFiles)
		}
	} else {
		// stdin, stdout and stderr at least.
		if usage.OpenFiles < 3 || usage.Handles != 0 {
			t.Errorf("unix: open_files %d handles %d", usage.OpenFiles, usage.Handles)
		}
	}
}

func TestSampleProcessUsage_CPUIsUserPlusSystemInCentiseconds(t *testing.T) {
	usage, err := sampleProcessUsage(context.Background(), processQueries{
		memory: func(context.Context) (*process.MemoryInfoStat, error) {
			return &process.MemoryInfoStat{RSS: 42}, nil
		},
		times: func(context.Context) (*cpu.TimesStat, error) {
			return &cpu.TimesStat{User: 1.234, System: 0.5}, nil
		},
		threads:     func(context.Context) (int32, error) { return 7, nil },
		descriptors: func(context.Context) (int32, error) { return 9, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if usage.RSSBytes != 42 || usage.CPUSeconds != 1.73 || usage.Threads != 7 {
		t.Errorf("usage %+v", usage)
	}
	if usage.OpenFiles+usage.Handles != 9 {
		t.Errorf("descriptors %+v, want 9 in the platform's field", usage)
	}
}

// A failed query leaves its field zero, names itself in the error, and the
// other fields are still read.
func TestSampleProcessUsage_AFailedQueryLeavesOnlyItsFieldZero(t *testing.T) {
	usage, err := sampleProcessUsage(context.Background(), processQueries{
		memory: func(context.Context) (*process.MemoryInfoStat, error) {
			return nil, errors.New("access denied")
		},
		times: func(context.Context) (*cpu.TimesStat, error) {
			return &cpu.TimesStat{User: 2}, nil
		},
		threads:     func(context.Context) (int32, error) { return 3, nil },
		descriptors: func(context.Context) (int32, error) { return 4, nil },
	})
	if err == nil || !strings.Contains(err.Error(), "resident memory query: access denied") {
		t.Errorf("want the memory query named in the error, got %v", err)
	}
	if usage.RSSBytes != 0 || usage.CPUSeconds != 2 || usage.Threads != 3 {
		t.Errorf("usage %+v", usage)
	}
}

// A query that never answers - and ignores its context, as the syscall-backed
// ones do - is abandoned at the shared budget: the sample returns within one
// bound, not one per query, with the stalled fields zero.
func TestSampleProcessUsage_StalledQueriesShareOneBudget(t *testing.T) {
	orig := hostCommandTimeoutOverrideStr
	hostCommandTimeoutOverrideStr = "200ms"
	t.Cleanup(func() { hostCommandTimeoutOverrideStr = orig })
	block := make(chan struct{})
	defer close(block)

	start := time.Now()
	usage, err := sampleProcessUsage(context.Background(), processQueries{
		memory: func(context.Context) (*process.MemoryInfoStat, error) {
			<-block
			return &process.MemoryInfoStat{RSS: 1}, nil
		},
		times: func(context.Context) (*cpu.TimesStat, error) {
			<-block
			return &cpu.TimesStat{User: 1}, nil
		},
		threads:     func(context.Context) (int32, error) { return 5, nil },
		descriptors: func(context.Context) (int32, error) { <-block; return 1, nil },
	})
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("the sample took %v; three stalled queries must share one 200ms budget", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) ||
		!strings.Contains(err.Error(), "resident memory query timed out after 200ms") {
		t.Errorf("want the stalled memory query named with the bound, got %v", err)
	}
	// The thread query runs after the budget has ended, so it is not run
	// either; the goroutine count needs no query.
	if usage.RSSBytes != 0 || usage.CPUSeconds != 0 || usage.Threads != 0 || usage.Goroutines < 1 {
		t.Errorf("usage %+v", usage)
	}
}
