//go:build darwin || linux

package interpreter

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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

// newBashExecutor returns a bash-backed executor that runs the user script file
// directly, mirroring the production Bash executor without the plugin wiring.
func newBashExecutor() *baseExecutor {
	return &baseExecutor{
		Shell:                    "bash",
		ShellVersionCheckCommand: "echo 1.0",
		WriteUtf8BOM:             false,
		BuildExecuteCommandArgs:  func(command string) []string { return []string{"-c", command} },
		BuildExecuteFileArgs:     func(path string) []string { return []string{path} },
		FS:                       utils.NewFileSystem(),
	}
}

func TestBaseExecutor_CommandTimeout_KillsHungScript(t *testing.T) {
	executor := newBashExecutor()

	var buf bytes.Buffer
	logger := hclog.New(&hclog.LoggerOptions{Output: &buf, Level: hclog.Error})
	timeout := 1
	device := agent.Device{RewstOrgId: "test-org-timeout", CommandTimeoutSeconds: &timeout}

	// A script that would otherwise block a worker indefinitely.
	msg := Message{PostId: "test:timeout", Commands: encodeCommand("sleep 30")}

	start := time.Now()
	done := make(chan []byte, 1)
	go func() {
		done <- executor.Execute(context.Background(), &msg, device, logger, nil, nil)
	}()

	var resultJSON []byte
	select {
	case resultJSON = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Execute did not return; command was not killed by timeout")
	}

	elapsed := time.Since(start)
	if elapsed > 5*time.Second {
		t.Errorf("command took %v to be killed; expected roughly the 1s timeout", elapsed)
	}

	var r result
	if err := json.Unmarshal(resultJSON, &r); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if !r.TimedOut {
		t.Errorf("expected timed_out=true, got result %s", resultJSON)
	}
	if !strings.Contains(r.Error, "timed out") {
		t.Errorf("expected error to mention timeout, got %q", r.Error)
	}

	// The timeout must be logged at Error level with the post_id for diagnosis.
	logs := buf.String()
	if !strings.Contains(logs, "Command timed out") {
		t.Errorf("expected an Error-level timeout log, got %q", logs)
	}
	if !strings.Contains(logs, "test:timeout") {
		t.Errorf("expected the timeout log to include the post_id, got %q", logs)
	}
}

func TestBaseExecutor_CommandTimeout_FastCommandUnaffected(t *testing.T) {
	executor := newBashExecutor()

	logger := hclog.New(&hclog.LoggerOptions{Output: &bytes.Buffer{}, Level: hclog.Error})
	timeout := 30
	device := agent.Device{RewstOrgId: "test-org-fast", CommandTimeoutSeconds: &timeout}

	msg := Message{PostId: "test:fast", Commands: encodeCommand("echo hello")}
	resultJSON := executor.Execute(context.Background(), &msg, device, logger, nil, nil)

	var r result
	if err := json.Unmarshal(resultJSON, &r); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if r.TimedOut {
		t.Errorf("fast command should not be flagged as timed out: %s", resultJSON)
	}
	if !strings.Contains(r.Output, "hello") {
		t.Errorf("expected command output to contain 'hello', got %q", r.Output)
	}
}

func TestBaseExecutor_CommandTimeout_UnboundedByDefault(t *testing.T) {
	executor := newBashExecutor()

	logger := hclog.New(&hclog.LoggerOptions{Output: &bytes.Buffer{}, Level: hclog.Error})
	// No CommandTimeoutSeconds set: execution is unbounded (historical behavior).
	device := agent.Device{RewstOrgId: "test-org-unbounded"}

	msg := Message{PostId: "test:unbounded", Commands: encodeCommand("sleep 1; echo done")}
	resultJSON := executor.Execute(context.Background(), &msg, device, logger, nil, nil)

	var r result
	if err := json.Unmarshal(resultJSON, &r); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if r.TimedOut {
		t.Errorf("command should not be timed out when no timeout is configured: %s", resultJSON)
	}
	if !strings.Contains(r.Output, "done") {
		t.Errorf("expected command output to contain 'done', got %q", r.Output)
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
