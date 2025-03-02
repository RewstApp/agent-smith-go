package interpreter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"runtime"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

type result struct {
	ExitCode int `json:"exitCode"`
}

func executeUsingPowershell(ctx context.Context, message *Message, device agent.Device) []byte {
	// Parse the commands
	commandBytes, err := base64.StdEncoding.DecodeString(*message.Commands)
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

	log.Println("Command", message.PostId, "saved to", tempfile.Name())

	// Close the temporary file
	tempfile.Close()

	cmd := exec.CommandContext(ctx, shell, "-File", tempfile.Name())
	err = cmd.Run()
	if err != nil {
		return errorResultBytes(err)
	}

	// Remove successfully executed temporary filename
	defer os.Remove(tempfile.Name())

	log.Println("Command", message.PostId, "completed with exit code:", cmd.ProcessState.ExitCode())

	result := result{ExitCode: cmd.ProcessState.ExitCode()}
	resultBytes, err := json.MarshalIndent(&result, "", "  ")
	if err != nil {
		return errorResultBytes(err)
	}

	return resultBytes
}
