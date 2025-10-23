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

const powershellVersionCheckCommand = "\"$($PSVersionTable.PSVersion.Major).$($PSVersionTable.PSVersion.Minor)\""

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

func executeUsingPowershell(ctx context.Context, message *Message, device agent.Device, logger hclog.Logger, usePwsh bool) []byte {
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
	shell := "powershell"
	if usePwsh {
		shell = "pwsh"
	}

	if logger.IsDebug() {
		cmd := exec.CommandContext(ctx, shell, "-Command", powershellVersionCheckCommand)
		combinedOutputBytes, err := cmd.CombinedOutput()
		combinedOutput := string(combinedOutputBytes)
		if err != nil {
			logger.Error("Shell version check failed", "error", err, "combined_output", combinedOutput)
		}

		version := strings.TrimSpace(combinedOutput)

		logger.Debug("Shell version", "shell", shell, "version", version)
		logger.Debug("Commands to execute", "commands", commands)
	}

	if logger.IsDebug() {
		cmd := exec.CommandContext(ctx, "whoami")
		combinedOutputBytes, err := cmd.CombinedOutput()
		combinedOutput := string(combinedOutputBytes)
		if err != nil {
			logger.Error("Whoami check failed", "error", err, "combined_output", combinedOutput)
		}

		logger.Debug("Whomai", "user", combinedOutput)
	}

	// Save commands to temporary file
	scriptsDir := agent.GetScriptsDirectory(device.RewstOrgId)
	err = utils.CreateFolderIfMissing(scriptsDir)
	if err != nil {
		return errorResultBytes(err)
	}

	tempfile, err := os.CreateTemp(scriptsDir, "exec-*.ps1")
	if err != nil {
		return errorResultBytes(err)
	}

	_, err = tempfile.Write(utf8BOM)
	if err != nil {
		logger.Error("Failed to write BOM", "error", err)
		return errorResultBytes(err)
	}

	_, err = tempfile.WriteString(commands)
	if err != nil {
		logger.Error("Failed to write command file", "error", err)
		return errorResultBytes(err)
	}

	logger.Info("Command saved to", "message_id", message.PostId, "path", tempfile.Name())

	// Close the temporary file
	tempfile.Close()

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, shell, "-File", tempfile.Name())
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	cmd.Env = os.Environ()
	cmd.Env = append(cmd.Env, fmt.Sprintf("AGENT_SMITH_VERSION=%s", version.Version[1:]))

	err = cmd.Run()
	if err != nil {
		logger.Error("Command failed", "error", err)
		logger.Debug("Command completed with outputs", "error", stderrBuf.String(), "info", stdoutBuf.String())
		return resultBytes(&result{Error: stderrBuf.String(), Output: stdoutBuf.String()})
	}

	// Remove successfully executed temporary filename
	defer os.Remove(tempfile.Name())

	logger.Info("Command completed", "message_id", message.PostId, "exit_code", cmd.ProcessState.ExitCode())
	logger.Debug("Command completed with outputs", "error", stderrBuf.String(), "info", stdoutBuf.String())

	return resultBytes(&result{Error: stderrBuf.String(), Output: stdoutBuf.String()})
}
