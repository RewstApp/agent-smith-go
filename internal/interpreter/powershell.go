package interpreter

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/RewstApp/agent-smith-go/internal/utils"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

func executeUsingPowershell(message *CommandDispatchMessage, conf utils.Config) error {
	// Parse the commands
	commandBytes, err := message.GetCommandBytes()
	if err != nil {
		return err
	}

	// Decode using UTF16LE
	decoder := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder()
	commands, _, err := transform.String(decoder, string(commandBytes))
	if err != nil {
		return err
	}
	log.Println("parsed commands:", commands)

	// Run the command in the system using powershell
	shell := "powershell"
	if runtime.GOOS != "windows" {
		shell = "pwsh"
	}

	// Save commands to temporary file
	baseDir, err := utils.BaseDirectory()
	if err != nil {
		return err
	}

	scriptsDir := filepath.Join(baseDir, "scripts")
	if !utils.DirExists(scriptsDir) {
		err = os.Mkdir(scriptsDir, 0755)
		if err != nil {
			return err
		}
	}

	tempfile, err := os.CreateTemp(scriptsDir, fmt.Sprintf("%s-*.ps1", message.PostId))
	if err != nil {
		return err
	}
	defer os.Remove(tempfile.Name())

	_, err = tempfile.WriteString(commands)
	if err != nil {
		return err
	}
	log.Println("Commands saved to temporary file:", tempfile.Name())

	// Close the temporary file
	tempfile.Close()
	cmd := exec.Command(shell, "-File", tempfile.Name())

	var errb, outb bytes.Buffer
	cmd.Stdout = &outb
	cmd.Stderr = &errb

	err = cmd.Run()
	if err != nil {
		log.Print("Stderr:", errb.String())
		log.Print("Stdout:", outb.String())
		return err
	}

	log.Print("Stdout:", outb.String())
	log.Println("Execution completed:", tempfile.Name())

	return nil
}
