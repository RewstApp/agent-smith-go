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

// TestBaseExecutor_OutputBelowCeiling_Unchanged pins the no-regression case: a
// command whose output fits under the ceiling produces exactly the result it
// produced before the bound existed, with no truncation keys on the wire.
func TestBaseExecutor_OutputBelowCeiling_Unchanged(t *testing.T) {
	executor := newBashExecutor()

	logger := hclog.New(&hclog.LoggerOptions{Output: &bytes.Buffer{}, Level: hclog.Warn})
	device := agent.Device{RewstOrgId: "test-org-below-ceiling"}

	msg := Message{
		PostId:   "test:below-ceiling",
		Commands: encodeCommand("printf 'hello\\n'; printf 'oops\\n' >&2"),
	}
	resultJSON := executor.Execute(context.Background(), &msg, device, logger, nil, nil)

	if want := `{"error":"oops\n","output":"hello\n"}`; string(resultJSON) != want {
		t.Errorf("result = %s, want %s", resultJSON, want)
	}
}

// TestBaseExecutor_OutputAboveCeiling_TruncatedAndFlagged covers the OOM case: a
// script writing far more than the ceiling must not be buffered in full, the
// command must still succeed, and the result must say what was dropped.
func TestBaseExecutor_OutputAboveCeiling_TruncatedAndFlagged(t *testing.T) {
	executor := newBashExecutor()

	var buf bytes.Buffer
	logger := hclog.New(&hclog.LoggerOptions{Output: &buf, Level: hclog.Warn})
	maxOutput := 1024
	device := agent.Device{RewstOrgId: "test-org-truncate", MaxOutputBytes: &maxOutput}

	// ~100 KB of stdout against a 1 KB ceiling, then a successful exit.
	msg := Message{
		PostId:   "test:truncate",
		Commands: encodeCommand("for i in $(seq 1 100); do printf 'x%.0s' $(seq 1 1024); done"),
	}
	resultJSON := executor.Execute(context.Background(), &msg, device, logger, nil, nil)

	var r result
	if err := json.Unmarshal(resultJSON, &r); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if !r.Truncated {
		t.Errorf("expected truncated=true, got %s", resultJSON)
	}
	if got := len(r.Output); got != maxOutput {
		t.Errorf("kept %d bytes of output, want the %d-byte ceiling", got, maxOutput)
	}
	if r.Output != strings.Repeat("x", maxOutput) {
		t.Error("kept output is not the leading bytes the command produced")
	}
	if r.OutputBytesKept != int64(maxOutput) {
		t.Errorf("output_bytes_kept = %d, want %d", r.OutputBytesKept, maxOutput)
	}
	if want := int64(100 * 1024); r.OutputBytesProduced != want {
		t.Errorf("output_bytes_produced = %d, want %d", r.OutputBytesProduced, want)
	}
	// A verbose command is not a failed command: it ran to completion, so no
	// timeout flag and no error.
	if r.TimedOut {
		t.Errorf("verbose command flagged as timed out: %s", resultJSON)
	}
	if r.Error != "" {
		t.Errorf("expected no error for a successful verbose command, got %q", r.Error)
	}

	// Exactly one Warn for the command, carrying the message id and both counts.
	logs := buf.String()
	if got := strings.Count(logs, "Command output truncated"); got != 1 {
		t.Errorf("expected exactly 1 truncation warning, got %d in %q", got, logs)
	}
	if !strings.Contains(logs, "[WARN]") {
		t.Errorf("expected the truncation log at Warn level, got %q", logs)
	}
	for _, want := range []string{
		"test:truncate",
		"output_bytes_produced=102400",
		"output_bytes_kept=1024",
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("expected truncation log to contain %q, got %q", want, logs)
		}
	}
}

// TestBaseExecutor_StderrBoundedIndependently proves one flooding stream cannot
// consume the other's budget, and that the result still tells them apart.
func TestBaseExecutor_StderrBoundedIndependently(t *testing.T) {
	executor := newBashExecutor()

	logger := hclog.New(&hclog.LoggerOptions{Output: &bytes.Buffer{}, Level: hclog.Warn})
	maxOutput := 512
	device := agent.Device{RewstOrgId: "test-org-stderr-bound", MaxOutputBytes: &maxOutput}

	// ~50 KB to stderr, a short line to stdout.
	msg := Message{
		PostId: "test:stderr-bound",
		Commands: encodeCommand(
			"for i in $(seq 1 50); do printf 'e%.0s' $(seq 1 1024) >&2; done; printf 'kept-stdout'",
		),
	}
	resultJSON := executor.Execute(context.Background(), &msg, device, logger, nil, nil)

	var r result
	if err := json.Unmarshal(resultJSON, &r); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if !r.Truncated {
		t.Errorf("expected truncated=true, got %s", resultJSON)
	}
	// stdout kept its own (small) output despite stderr blowing past the ceiling.
	if r.Output != "kept-stdout" {
		t.Errorf("output = %q, want %q", r.Output, "kept-stdout")
	}
	if got := len(r.Error); got != maxOutput {
		t.Errorf("kept %d bytes of stderr, want the %d-byte ceiling", got, maxOutput)
	}
	if r.Error != strings.Repeat("e", maxOutput) {
		t.Error("kept stderr is not the leading bytes the command produced")
	}
	if want := int64(50*1024 + len("kept-stdout")); r.OutputBytesProduced != want {
		t.Errorf("output_bytes_produced = %d, want %d (both streams)", r.OutputBytesProduced, want)
	}
	if want := int64(maxOutput + len("kept-stdout")); r.OutputBytesKept != want {
		t.Errorf("output_bytes_kept = %d, want %d (both streams)", r.OutputBytesKept, want)
	}
}

// TestBaseExecutor_TruncationComposesWithTimeout covers the interaction of the two
// bounds: a script that floods output and then hangs must be killed at its
// deadline and report both flags, with its worker released.
func TestBaseExecutor_TruncationComposesWithTimeout(t *testing.T) {
	executor := newBashExecutor()

	var buf bytes.Buffer
	logger := hclog.New(&hclog.LoggerOptions{Output: &buf, Level: hclog.Warn})
	timeout := 1
	maxOutput := 1024
	device := agent.Device{
		RewstOrgId:            "test-org-truncate-timeout",
		CommandTimeoutSeconds: &timeout,
		MaxOutputBytes:        &maxOutput,
	}

	msg := Message{
		PostId: "test:truncate-timeout",
		Commands: encodeCommand(
			"for i in $(seq 1 20); do printf 'x%.0s' $(seq 1 1024); done; sleep 30",
		),
	}

	done := make(chan []byte, 1)
	go func() {
		done <- executor.Execute(context.Background(), &msg, device, logger, nil, nil)
	}()

	var resultJSON []byte
	select {
	case resultJSON = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("Execute did not return; the worker was not released")
	}

	var r result
	if err := json.Unmarshal(resultJSON, &r); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if !r.TimedOut {
		t.Errorf("expected timed_out=true, got %s", resultJSON)
	}
	if !r.Truncated {
		t.Errorf("expected truncated=true, got %s", resultJSON)
	}
	if got := len(r.Output); got != maxOutput {
		t.Errorf("kept %d bytes of output, want the %d-byte ceiling", got, maxOutput)
	}
	if r.OutputBytesProduced <= int64(maxOutput) {
		t.Errorf(
			"output_bytes_produced = %d, want more than the %d-byte ceiling",
			r.OutputBytesProduced,
			maxOutput,
		)
	}
	if got := strings.Count(buf.String(), "Command output truncated"); got != 1 {
		t.Errorf("expected exactly 1 truncation warning, got %d in %q", got, buf.String())
	}
}

// TestBaseExecutor_DefaultCeilingKeepsLargeLegitimateOutput checks the default is
// permissive enough that output a real workflow might return is kept in full when
// max_output_bytes is left unset.
func TestBaseExecutor_DefaultCeilingKeepsLargeLegitimateOutput(t *testing.T) {
	executor := newBashExecutor()

	var buf bytes.Buffer
	logger := hclog.New(&hclog.LoggerOptions{Output: &buf, Level: hclog.Warn})
	device := agent.Device{RewstOrgId: "test-org-default-ceiling"}

	// 1 MB, well under the 10 MiB default.
	msg := Message{
		PostId:   "test:default-ceiling",
		Commands: encodeCommand("for i in $(seq 1 1024); do printf 'x%.0s' $(seq 1 1024); done"),
	}
	resultJSON := executor.Execute(context.Background(), &msg, device, logger, nil, nil)

	var r result
	if err := json.Unmarshal(resultJSON, &r); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}

	if r.Truncated {
		t.Errorf("1 MB of output was truncated under the default ceiling: %s", resultJSON[:64])
	}
	if got, want := len(r.Output), 1024*1024; got != want {
		t.Errorf("kept %d bytes, want all %d", got, want)
	}
	if strings.Contains(buf.String(), "Command output truncated") {
		t.Errorf("unexpected truncation warning: %q", buf.String())
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
