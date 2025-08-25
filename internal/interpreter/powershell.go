package interpreter

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"runtime"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/hashicorp/go-hclog"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

func executeUsingPowershell(ctx context.Context, message *Message, device agent.Device, logger hclog.Logger) []byte {
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
	if runtime.GOOS != "windows" {
		shell = "pwsh"
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

	_, err = tempfile.WriteString(commands)
	if err != nil {
		return errorResultBytes(err)
	}

	logger.Info("Command saved to", "message_id", message.PostId, "path", tempfile.Name())

	// Close the temporary file
	tempfile.Close()

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd := exec.CommandContext(ctx, shell, "-File", tempfile.Name())
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err = cmd.Run()
	if err != nil {
		return resultBytes(&result{Error: stderrBuf.String(), Output: stdoutBuf.String()})
	}

	// Remove successfully executed temporary filename
	defer os.Remove(tempfile.Name())

	logger.Info("Command completed", "message_id", message.PostId, "exit_code", cmd.ProcessState.ExitCode())

	return resultBytes(&result{Error: stderrBuf.String(), Output: stdoutBuf.String()})
}
