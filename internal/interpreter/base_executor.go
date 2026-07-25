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

	// Save commands to temporary file
	scriptsDir := agent.GetScriptsDirectory(device.RewstOrgId)
	err = e.FS.MkdirAll(scriptsDir)
	if err != nil {
		return errorResultBytes(logger, err)
	}

	tempfile, err := os.CreateTemp(scriptsDir, "exec-*.ps1")
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

	var stdoutBuf, stderrBuf bytes.Buffer

	// Bound the command to the configured per-command timeout when one is set, so
	// a hung or interactive script (infinite loop, blocked on stdin, stuck network
	// call) is killed after the deadline instead of permanently occupying its
	// worker. When no timeout is configured, execCtx is the unmodified per-cycle
	// ctx and the command remains unbounded (historical behavior).
	execCtx := ctx
	if timeout, ok := device.ResolvedCommandTimeout(); ok {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// #nosec G204
	cmd := exec.CommandContext(execCtx, e.Shell, e.BuildExecuteFileArgs(tempfile.Name())...)
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("AGENT_SMITH_VERSION=%s", version.Version[1:]))

	// Kill the whole process group on cancellation (see configureProcessGroup) so
	// a shell that spawned children is fully torn down, and bound how long Run may
	// block on output pipes afterward. Because stdout/stderr are in-memory buffers
	// (not *os.File), the runtime copies them through a pipe a killed child can
	// still hold open; WaitDelay guarantees Run returns and the worker is released
	// even then. This only takes effect when the context is cancelled, so commands
	// that finish on their own are unaffected.
	configureProcessGroup(cmd)
	cmd.WaitDelay = commandWaitDelay

	err = cmd.Run()
	if err != nil {
		// Distinguish a command killed by the per-command timeout from a normal
		// non-zero exit. execCtx exceeding its deadline while the parent ctx is
		// still live means the timeout fired (not a service stop / reconnect,
		// which cancels the parent ctx instead).
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
			timeout, _ := device.ResolvedCommandTimeout()
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
			return timeoutResultBytes(logger, errMsg, stdoutBuf.String())
		}

		logger.Error("Command failed", "error", err)
		logger.Debug(
			"Command completed with outputs",
			"error",
			stderrBuf.String(),
			"info",
			stdoutBuf.String(),
		)
		return resultBytes(logger, stderrBuf.String(), stdoutBuf.String())
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

	return resultBytes(logger, stderrBuf.String(), stdoutBuf.String())
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
