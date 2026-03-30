package interpreter

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
	"github.com/hashicorp/go-hclog"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

type baseExecutor struct {
	Shell                    string
	ShellVersionCheckCommand string
	WriteUtf8BOM             bool
	BuildExecuteCommandArgs  BuildExecuteCommandArgsFunc
	BuildExecuteFileArgs     BuildExecuteFileArgsFunc
	FS                       utils.FileSystem
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
		return errorResultBytes(err)
	}

	// Decode using UTF16LE
	decoder := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder()
	commands, _, err := transform.String(decoder, string(commandBytes))
	if err != nil {
		return errorResultBytes(err)
	}

	// Run the command in the system using powershell
	if logger.IsDebug() {
		// #nosec G204
		cmd := exec.CommandContext(
			ctx,
			e.Shell,
			e.BuildExecuteCommandArgs(e.ShellVersionCheckCommand)...)
		combinedOutputBytes, err := cmd.CombinedOutput()
		combinedOutput := string(combinedOutputBytes)
		if err != nil {
			logger.Error(
				"Shell version check failed",
				"error",
				err,
				"combined_output",
				combinedOutput,
			)
		}

		version := strings.TrimSpace(combinedOutput)

		logger.Debug("Shell version", "shell", e.Shell, "version", version)
		logger.Debug("Commands to execute", "commands", commands)
	}

	if logger.IsDebug() {
		// #nosec G204
		cmd := exec.CommandContext(ctx, e.Shell, e.BuildExecuteCommandArgs("whoami")...)
		combinedOutputBytes, err := cmd.CombinedOutput()
		combinedOutput := string(combinedOutputBytes)
		if err != nil {
			logger.Error("Whoami check failed", "error", err, "combined_output", combinedOutput)
		}

		logger.Debug("Whoami", "user", combinedOutput)
	}

	// Save commands to temporary file
	scriptsDir := agent.GetScriptsDirectory(device.RewstOrgId)
	err = e.FS.MkdirAll(scriptsDir)
	if err != nil {
		return errorResultBytes(err)
	}

	tempfile, err := os.CreateTemp(scriptsDir, "exec-*.ps1")
	if err != nil {
		return errorResultBytes(err)
	}

	if e.WriteUtf8BOM {
		_, err = tempfile.Write(utf8BOM)
		if err != nil {
			logger.Error("Failed to write BOM", "error", err)
			return errorResultBytes(err)
		}
	}

	_, err = tempfile.WriteString(commands)
	if err != nil {
		logger.Error("Failed to write command file", "error", err)
		return errorResultBytes(err)
	}

	logger.Info("Command saved to", "message_id", message.PostId, "path", tempfile.Name())

	// Close the temporary file
	err = tempfile.Close()
	if err != nil {
		logger.Error("Failed to close temp file handle", "error", err)
		return errorResultBytes(err)
	}

	var stdoutBuf, stderrBuf bytes.Buffer

	// #nosec G204
	cmd := exec.CommandContext(ctx, e.Shell, e.BuildExecuteFileArgs(tempfile.Name())...)
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("AGENT_SMITH_VERSION=%s", version.Version[1:]))

	err = cmd.Run()
	if err != nil {
		logger.Error("Command failed", "error", err)
		logger.Debug(
			"Command completed with outputs",
			"error",
			stderrBuf.String(),
			"info",
			stdoutBuf.String(),
		)
		return resultBytes(stderrBuf.String(), stdoutBuf.String())
	}

	// Remove successfully executed temporary filename
	defer func() {
		err = os.Remove(tempfile.Name())
		if err != nil {
			logger.Error("Failed to remove temp file", "file", tempfile.Name(), "error", err)
		}
	}()

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

	return resultBytes(stderrBuf.String(), stdoutBuf.String())
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
