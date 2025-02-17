package interpreter

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/RewstApp/agent-smith-go/internal/utils"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

func sanitizeFilename(filename string) string {
	return strings.Map(func(r rune) rune {
		// Replace prohibited characters in windows
		if r == '<' || r == '>' || r == ':' || r == '"' || r == '/' || r == '\\' || r == '|' || r == '?' || r == '*' {
			return '_'
		}

		// Do not replace the character
		return r
	}, filename)
}

func executeUsingPowershell(message *CommandDispatchMessage) (CommandDispatchResult, error) {
	// Parse the commands
	commandBytes, err := message.GetCommandBytes()
	if err != nil {
		return CommandDispatchResult{}, err
	}

	// Decode using UTF16LE
	decoder := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder()
	commands, _, err := transform.String(decoder, string(commandBytes))
	if err != nil {
		return CommandDispatchResult{}, err
	}

	// Run the command in the system using powershell
	shell := "powershell"
	if runtime.GOOS != "windows" {
		shell = "pwsh"
	}

	// Save commands to temporary file
	baseDir, err := utils.BaseDirectory()
	if err != nil {
		return CommandDispatchResult{}, err
	}

	scriptsDir := filepath.Join(baseDir, "scripts")
	if !utils.DirExists(scriptsDir) {
		err = os.Mkdir(scriptsDir, 0755)
		if err != nil {
			return CommandDispatchResult{}, err
		}
	}

	tempfile, err := os.CreateTemp(scriptsDir, fmt.Sprintf("%s-*.ps1", sanitizeFilename(message.PostId)))
	if err != nil {
		return CommandDispatchResult{}, err
	}
	defer os.Remove(tempfile.Name())

	_, err = tempfile.WriteString(commands)
	if err != nil {
		return CommandDispatchResult{}, err
	}

	// Close the temporary file
	tempfile.Close()
	cmd := exec.Command(shell, "-File", tempfile.Name())

	var errb, outb bytes.Buffer
	cmd.Stdout = &outb
	cmd.Stderr = &errb

	err = cmd.Run()
	if err != nil {
		return CommandDispatchResult{}, err
	}

	return CommandDispatchResult{
		message.PostId,
		shell,
		tempfile.Name(),
		cmd.ProcessState.ExitCode(),
		errb.String(),
		outb.String(),
	}, nil
}
