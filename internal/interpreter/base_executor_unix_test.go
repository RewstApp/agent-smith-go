//go:build darwin || linux

package interpreter

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/hashicorp/go-hclog"
)

// newCountingExecutor returns a bash-backed executor whose shell-version check
// appends a byte to counterFile each time it actually runs as a subprocess.
// Counting the version check is sufficient to prove how many times the cached
// diagnostics were computed, because the version check and whoami share a single
// sync.Once. The real script execution writes the user commands to a temp file
// and never touches counterFile, so it does not interfere with the count.
func newCountingExecutor(counterFile string) *baseExecutor {
	return &baseExecutor{
		Shell:                    "bash",
		ShellVersionCheckCommand: "printf x >> " + counterFile + "; echo 1.0",
		WriteUtf8BOM:             false,
		BuildExecuteCommandArgs:  func(command string) []string { return []string{"-c", command} },
		BuildExecuteFileArgs:     func(path string) []string { return []string{path} },
		FS:                       utils.NewFileSystem(),
	}
}

func countSubprocessRuns(t *testing.T, counterFile string) int {
	t.Helper()
	data, err := os.ReadFile(counterFile)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatalf("failed to read counter file: %v", err)
	}
	return len(data)
}

func TestBaseExecutor_Diagnostics_CachedAcrossCommands(t *testing.T) {
	counterFile := filepath.Join(t.TempDir(), "version-runs")
	executor := newCountingExecutor(counterFile)

	var buf bytes.Buffer
	logger := hclog.New(&hclog.LoggerOptions{Output: &buf, Level: hclog.Debug})
	device := agent.Device{RewstOrgId: "test-org-cache"}

	const commandCount = 3
	for i := 0; i < commandCount; i++ {
		msg := Message{PostId: "test:cache", Commands: encodeCommand("echo hello")}
		executor.Execute(context.Background(), &msg, device, logger, nil, nil)
	}

	if runs := countSubprocessRuns(t, counterFile); runs != 1 {
		t.Errorf("expected version-check subprocess to run once, ran %d times", runs)
	}

	logs := strings.ToLower(buf.String())
	// Each command still logs the (cached) diagnostics, so debug output is unchanged.
	if got := strings.Count(logs, "[debug] shell version"); got != commandCount {
		t.Errorf("expected %d shell version log lines, got %d", commandCount, got)
	}
	if got := strings.Count(logs, "[debug] whoami"); got != commandCount {
		t.Errorf("expected %d whoami log lines, got %d", commandCount, got)
	}
}

func TestBaseExecutor_Diagnostics_DisabledAtInfoLevel(t *testing.T) {
	counterFile := filepath.Join(t.TempDir(), "version-runs")
	executor := newCountingExecutor(counterFile)

	logger := hclog.New(&hclog.LoggerOptions{Output: &bytes.Buffer{}, Level: hclog.Info})
	device := agent.Device{RewstOrgId: "test-org-info"}

	msg := Message{PostId: "test:info", Commands: encodeCommand("echo hello")}
	executor.Execute(context.Background(), &msg, device, logger, nil, nil)

	if runs := countSubprocessRuns(t, counterFile); runs != 0 {
		t.Errorf("expected no diagnostic subprocess at info level, ran %d times", runs)
	}
}

func TestBaseExecutor_Diagnostics_ConcurrentCachedOnce(t *testing.T) {
	counterFile := filepath.Join(t.TempDir(), "version-runs")
	executor := newCountingExecutor(counterFile)

	logger := hclog.New(&hclog.LoggerOptions{Output: &bytes.Buffer{}, Level: hclog.Debug})
	device := agent.Device{RewstOrgId: "test-org-concurrent"}

	const workers = 10
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			msg := Message{PostId: "test:concurrent", Commands: encodeCommand("echo hello")}
			executor.Execute(context.Background(), &msg, device, logger, nil, nil)
		}()
	}
	wg.Wait()

	if runs := countSubprocessRuns(t, counterFile); runs != 1 {
		t.Errorf(
			"expected version-check subprocess to run once under concurrency, ran %d times",
			runs,
		)
	}
}
