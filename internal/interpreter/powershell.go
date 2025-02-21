package interpreter

import (
	"context"
	"encoding/base64"
	"log"
	"os"
	"os/exec"
	"runtime"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

func executeUsingPowershell(ctx context.Context, message *Message, device *agent.Device) error {
	// Parse the commands
	commandBytes, err := base64.StdEncoding.DecodeString(*message.Commands)
	if err != nil {
		return err
	}

	// Decode using UTF16LE
	decoder := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder()
	commands, _, err := transform.String(decoder, string(commandBytes))
	if err != nil {
		return err
	}

	// Run the command in the system using powershell
	shell := "powershell"
	if runtime.GOOS != "windows" {
		shell = "pwsh"
	}

	// Save commands to temporary file
	scriptsDir, err := agent.GetScriptsDirectory(device.RewstOrgId)
	if err != nil {
		return err
	}

	tempfile, err := os.CreateTemp(scriptsDir, "exec-*.ps1")
	if err != nil {
		return err
	}

	_, err = tempfile.WriteString(commands)
	if err != nil {
		return err
	}

	log.Println("Commands saved to", tempfile.Name())

	// Close the temporary file
	tempfile.Close()
	cmd := exec.CommandContext(ctx, shell, "-File", tempfile.Name())
	cmd.Stdout = log.Writer()
	cmd.Stderr = log.Writer()

	err = cmd.Run()
	if err != nil {
		return err
	}

	// Remove successfully executed temporary filename
	defer os.Remove(tempfile.Name())

	log.Println("Command", message.PostId, "completed with exit code", cmd.ProcessState.ExitCode())

	return nil
}
