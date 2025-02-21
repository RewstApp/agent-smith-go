package interpreter

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

func executeUsingPowershell(ctx context.Context, message *Message, device *agent.Device) (*Result, error) {
	// Parse the commands
	commandBytes, err := base64.StdEncoding.DecodeString(*message.Commands)
	if err != nil {
		return nil, err
	}

	// Decode using UTF16LE
	decoder := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder()
	commands, _, err := transform.String(decoder, string(commandBytes))
	if err != nil {
		return nil, err
	}

	// Run the command in the system using powershell
	shell := "powershell"
	if runtime.GOOS != "windows" {
		shell = "pwsh"
	}

	// Save commands to temporary file
	baseDir, err := utils.BaseDirectory()
	if err != nil {
		return nil, err
	}

	scriptsDir := filepath.Join(baseDir, "scripts")
	if !utils.DirExists(scriptsDir) {
		err = os.Mkdir(scriptsDir, 0755)
		if err != nil {
			return nil, err
		}
	}

	tempfile, err := os.CreateTemp(scriptsDir, "exec-*.ps1")
	if err != nil {
		return nil, err
	}

	_, err = tempfile.WriteString(commands)
	if err != nil {
		return nil, err
	}

	// Close the temporary file
	tempfile.Close()
	cmd := exec.CommandContext(ctx, shell, "-File", tempfile.Name())

	var errb, outb bytes.Buffer
	cmd.Stdout = &outb
	cmd.Stderr = &errb

	err = cmd.Run()
	if err != nil {
		return nil, err
	}

	// Remove successfully executed temporary filename
	defer os.Remove(tempfile.Name())

	return &Result{
		PostId: message.PostId,
		Commands: &CommandsResult{
			Interpreter:  shell,
			TempFilename: tempfile.Name(),
			ExitCode:     cmd.ProcessState.ExitCode(),
			Stderr:       errb.String(),
			Stdout:       outb.String(),
		},
		GetInstallation: nil,
	}, nil
}
