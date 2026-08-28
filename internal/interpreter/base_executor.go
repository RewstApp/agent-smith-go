package interpreter

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
	"github.com/hashicorp/go-hclog"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// verifyScriptUnchanged re-reads the script file at path and compares it
// against expected, the exact bytes Execute wrote to it. Close and the
// shell's own subsequent open of the same path are two independent opens of
// the file; this is what catches a swap in that window — refusing to run the
// command rather than trusting whatever the path now resolves to — instead
// of relying solely on directory permissions never having been momentarily
// wrong.
func verifyScriptUnchanged(path string, expected []byte) error {
	onDisk, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if !bytes.Equal(onDisk, expected) {
		return fmt.Errorf("command script %s changed after write; refusing to execute", path)
	}
	return nil
}

// commandWaitDelay bounds how long cmd.Wait blocks on the output pipes after the
// command's context is cancelled and the process (group) is killed. It is a
// backstop for a child that briefly holds the inherited stdout/stderr pipe open;
// once it elapses the runtime force-closes the pipes so the worker is released.
const commandWaitDelay = 10 * time.Second

type baseExecutor struct {
	Shell                    string
	ShellVersionCheckCommand string
	WriteUtf8BOM             bool
	BuildExecuteCommandArgs  BuildExecuteCommandArgsFunc
	BuildExecuteFileArgs     BuildExecuteFileArgsFunc
	FS                       utils.FileSystem

	// Diagnostic values (shell version and the service account reported by
	// whoami) are static for the lifetime of an agent process: the shell binary
	// and the account it runs as do not change between commands. They are
	// therefore computed at most once via diagOnce and reused on every
	// subsequent command instead of spawning two extra subprocesses per
	// command. diagOnce makes the computation safe under the concurrent worker
	// pool, and the cached fields are only read after Do returns (so the
	// once-guaranteed happens-before relationship protects them from races).
	diagOnce      sync.Once
	cachedVersion string
	cachedWhoami  string
}

// diagnostics returns the shell version and current-user strings used for debug
// logging, computing them via two subprocesses the first time it is called and
// returning the memoized values thereafter. It is only invoked when debug
// logging is enabled, so info-level operation never spawns these subprocesses.
func (e *baseExecutor) diagnostics(ctx context.Context, logger hclog.Logger) (string, string) {
	e.diagOnce.Do(func() {
		// #nosec G204
		versionCmd := exec.CommandContext(
			ctx,
			e.Shell,
			e.BuildExecuteCommandArgs(e.ShellVersionCheckCommand)...)
		versionOutputBytes, err := versionCmd.CombinedOutput()
		versionOutput := string(versionOutputBytes)
		if err != nil {
			logger.Error(
				"Shell version check failed",
				"error",
				err,
				"combined_output",
				versionOutput,
			)
		}
		e.cachedVersion = strings.TrimSpace(versionOutput)

		// #nosec G204
		whoamiCmd := exec.CommandContext(ctx, e.Shell, e.BuildExecuteCommandArgs("whoami")...)
		whoamiOutputBytes, err := whoamiCmd.CombinedOutput()
		whoamiOutput := string(whoamiOutputBytes)
		if err != nil {
			logger.Error("Whoami check failed", "error", err, "combined_output", whoamiOutput)
		}
		e.cachedWhoami = whoamiOutput
	})

	return e.cachedVersion, e.cachedWhoami
}

// SECURITY: Agent Smith is a command execution agent. The shell executable is configured
// via device settings (not arbitrary user input) and command arguments are constructed
// by trusted internal methods. This is the intended and documented behavior.
func (e *baseExecutor) Execute(
	ctx context.Context,
	message *Message,
	device agent.Device,
	logger hclog.Logger,
	sys agent.SystemInfoProvider,
	domain agent.DomainInfoProvider,
) []byte {
	// Parse the commands
	commandBytes, err := base64.StdEncoding.DecodeString(message.Commands)
	if err != nil {
		return errorResultBytes(logger, err)
	}

	// Decode using UTF16LE
	decoder := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder()
	commands, _, err := transform.String(decoder, string(commandBytes))
	if err != nil {
		return errorResultBytes(logger, err)
	}

	// Log diagnostics in debug mode. The shell version and whoami values are
	// computed once per agent process and reused; only the per-command output
	// (the commands themselves) varies between calls.
	if logger.IsDebug() {
		version, user := e.diagnostics(ctx, logger)

		logger.Debug("Shell version", "shell", e.Shell, "version", version)
		logger.Debug("Commands to execute", "commands", commands)
		logger.Debug("Whoami", "user", user)
	}

	// Save commands to temporary file. The directory is agent-owned (not
	// shared system temp) and EnsureSecureDir re-asserts its restrictive
	// ownership/mode on every call rather than trusting a pre-existing
	// directory as-is, so a local unprivileged user can no longer pre-plant it
	// with permissive ownership. See agent.GetScriptsDirectory and
	// utils.EnsureSecureDir.
	scriptsDir := agent.GetScriptsDirectory(device.RewstOrgId)
	err = e.FS.EnsureSecureDir(scriptsDir)
	if err != nil {
		return errorResultBytes(logger, err)
	}

	tempfile, err := os.CreateTemp(scriptsDir, scriptTempPattern)
	if err != nil {
		return errorResultBytes(logger, err)
	}

	// Single cleanup: close the handle (Windows blocks Remove on open files), then
	// remove the file. Runs on every exit path. ErrClosed is expected on the success
	// path because we close explicitly before exec.
	defer func() {
		name := tempfile.Name()
		if err := tempfile.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
			logger.Error("Failed to close temp file", "file", name, "error", err)
		}
		if err := os.Remove(name); err != nil {
			logger.Error("Failed to remove temp file", "file", name, "error", err)
		}
	}()

	// expectedContent is exactly what is written below, kept so it can be
	// compared against what is actually on disk immediately before exec (see
	// the integrity check after Close): the two writes below and the exec
	// call after Close each open the file independently, and confirming the
	// bytes are unchanged is what catches a swap in that window rather than
	// trusting whatever a path re-open returns.
	var expectedContent []byte
	if e.WriteUtf8BOM {
		expectedContent = append(expectedContent, utf8BOM...)
	}
	expectedContent = append(expectedContent, []byte(commands)...)

	if e.WriteUtf8BOM {
		_, err = tempfile.Write(utf8BOM)
		if err != nil {
			logger.Error("Failed to write BOM", "error", err)
			return errorResultBytes(logger, err)
		}
	}

	_, err = tempfile.WriteString(commands)
	if err != nil {
		logger.Error("Failed to write command file", "error", err)
		return errorResultBytes(logger, err)
	}

	logger.Info("Command saved to", "message_id", message.PostId, "path", tempfile.Name())

	// Close explicitly before exec so the shell can open the script (required on Windows).
	// The deferred cleanup will still run Remove; its Close becomes a no-op (ErrClosed).
	if err := tempfile.Close(); err != nil {
		logger.Error("Failed to close temp file handle", "error", err)
		return errorResultBytes(logger, err)
	}

	// Re-read the file by path and compare it against what was just written.
	// Close above and the shell's own open of the same path below are two
	// independent opens of the file; EnsureSecureDir keeps any other local
	// account from being able to write into this directory at all, and this
	// check catches it — refusing to execute rather than running
	// attacker-controlled content — on the off chance content still changed
	// in that window (e.g. the directory's mode was briefly wrong).
	if err := verifyScriptUnchanged(tempfile.Name(), expectedContent); err != nil {
		logger.Error(
			"Command script integrity check failed",
			"message_id", message.PostId,
			"path", tempfile.Name(),
			"error", err,
		)
		return errorResultBytes(logger, err)
	}

	// Capture stdout and stderr through independently bounded writers so a script
	// that writes an unbounded volume of output cannot grow the agent's heap until
	// the service is OOM-killed and the endpoint drops offline. Output past the
	// ceiling is discarded rather than accumulated, which is invisible to the
	// command itself: it still runs to completion (or to its deadline) and
	// everything it produced up to the ceiling is still delivered.
	maxOutputBytes := device.ResolvedMaxOutputBytes()
	stdoutBuf := newBoundedWriter(maxOutputBytes)
	stderrBuf := newBoundedWriter(maxOutputBytes)

	// Bound the command to the configured (or default) per-command timeout, so a
	// hung or interactive script (infinite loop, blocked on stdin, stuck network
	// call) is killed after the deadline instead of permanently occupying its
	// worker.
	execCtx, cancel := context.WithTimeout(ctx, device.ResolvedCommandTimeout())
	defer cancel()

	// #nosec G204
	cmd := exec.CommandContext(execCtx, e.Shell, e.BuildExecuteFileArgs(tempfile.Name())...)
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("AGENT_SMITH_VERSION=%s", version.Version[1:]))

	// Kill the whole descendant tree on cancellation (a process group on Unix, a
	// job object on Windows — see configureProcessGroup) so a shell that spawned
	// children is fully torn down, and bound how long Wait may block on output
	// pipes afterward. Because stdout/stderr are in-memory writers (not
	// *os.File), the runtime copies them through a pipe a killed child can still
	// hold open; WaitDelay guarantees Wait returns and the worker is released
	// even then. This only takes effect when the context is cancelled, so
	// commands that finish on their own are unaffected.
	tree := configureProcessGroup(cmd)
	cmd.WaitDelay = commandWaitDelay

	// Split Run into Start+Wait so the Windows job-object assignment (tree.Assign)
	// can happen as soon as the process exists, minimizing the window in which a
	// very fast child could be spawned before it is captured by the job.
	if err = cmd.Start(); err == nil {
		if assignErr := tree.Assign(); assignErr != nil {
			logger.Warn(
				"Failed to fully isolate command process tree; child processes may survive a timeout",
				"message_id",
				message.PostId,
				"error",
				assignErr,
			)
		}
		err = cmd.Wait()
	}
	if releaseErr := tree.Release(); releaseErr != nil {
		logger.Warn(
			"Failed to release command process tree resources",
			"message_id", message.PostId,
			"error", releaseErr,
		)
	}

	// Report discarded output once per command — never per write — and before any
	// result is built, so every return path below carries the same signal.
	trunc := truncationOf(stdoutBuf, stderrBuf)
	if trunc.Truncated {
		logger.Warn(
			"Command output truncated",
			"message_id",
			message.PostId,
			"max_output_bytes",
			maxOutputBytes,
			"output_bytes_produced",
			trunc.Produced,
			"output_bytes_kept",
			trunc.Kept,
		)
	}

	if err != nil {
		// Distinguish a command killed by the per-command timeout from a normal
		// non-zero exit. execCtx exceeding its deadline while the parent ctx is
		// still live means the timeout fired (not a service stop / reconnect,
		// which cancels the parent ctx instead).
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			timeout := device.ResolvedCommandTimeout()
			logger.Error(
				"Command timed out",
				"message_id",
				message.PostId,
				"timeout",
				timeout,
			)
			logger.Debug(
				"Command timed out with outputs",
				"error",
				stderrBuf.String(),
				"info",
				stdoutBuf.String(),
			)
			errMsg := fmt.Sprintf("command timed out after %s", timeout)
			if stderrBuf.Len() > 0 {
				errMsg = fmt.Sprintf("%s: %s", errMsg, stderrBuf.String())
			}
			return timeoutResultBytes(logger, errMsg, stdoutBuf.String(), trunc)
		}

		logger.Error("Command failed", "error", err)
		logger.Debug(
			"Command completed with outputs",
			"error",
			stderrBuf.String(),
			"info",
			stdoutBuf.String(),
		)
		return resultBytes(logger, stderrBuf.String(), stdoutBuf.String(), trunc)
	}

	logger.Info(
		"Command completed",
		"message_id",
		message.PostId,
		"exit_code",
		cmd.ProcessState.ExitCode(),
	)
	logger.Debug(
		"Command completed with outputs",
		"error",
		stderrBuf.String(),
		"info",
		stdoutBuf.String(),
	)

	return resultBytes(logger, stderrBuf.String(), stdoutBuf.String(), trunc)
}

func (e *baseExecutor) AlwaysPostback() bool {
	return false
}

func NewBaseExecutor(
	shell string,
	shellVersionCheckCommand string,
	writeUtf8BOM bool,
	buildExecuteCommandArgs BuildExecuteCommandArgsFunc,
	buildExecuteFileArgs BuildExecuteFileArgsFunc,
	fs utils.FileSystem,
) Executor {
	return &baseExecutor{
		Shell:                    shell,
		ShellVersionCheckCommand: shellVersionCheckCommand,
		WriteUtf8BOM:             writeUtf8BOM,
		BuildExecuteCommandArgs:  buildExecuteCommandArgs,
		BuildExecuteFileArgs:     buildExecuteFileArgs,
		FS:                       fs,
	}
}
