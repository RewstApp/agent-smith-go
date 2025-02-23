package interpreter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/RewstApp/agent-smith-go/internal/agent"
)

type Message struct {
	PostId              string  `json:"post_id"`
	Commands            *string `json:"commands"`
	InterpreterOverride *string `json:"interpreter_override"`
	GetInstallation     *bool   `json:"get_installation"`
}

type CommandsResult struct {
	Interpreter  string
	TempFilename string
	ExitCode     int
	Stderr       string
	Stdout       string
}

type GetInstallationResult struct {
	StatusCode int
}

type Result struct {
	PostId          string
	Commands        *CommandsResult
	GetInstallation *GetInstallationResult
}

func (msg *Message) Parse(data []byte) error {
	return json.Unmarshal(data, msg)
}

func (msg *Message) Execute(ctx context.Context, device *agent.Device) error {
	// Execute commands if given
	if msg.Commands != nil {
		log.Println("Executing commands...")

		// Select the correct interpreter
		switch msg.InterpreterOverride {
		// TODO: Support other interpreter
		default:
			return executeUsingPowershell(ctx, msg, device)
		}
	}

	// Get installation data if given
	if msg.GetInstallation != nil && *msg.GetInstallation {
		log.Println("Executing get_installation...")

		// Create a postback url
		postBackUrl := fmt.Sprintf("https://%s/webhooks/custom/action/%s", device.RewstEngineHost, strings.ReplaceAll(msg.PostId, ":", "/"))

		// Load the paths data
		var paths agent.PathsData
		err := paths.Load(ctx, device.RewstOrgId)
		if err != nil {
			return err
		}

		// Convert to bytes in json
		pathsBytes, err := json.MarshalIndent(&paths, "", "  ")
		if err != nil {
			return err
		}

		// Create an http request
		req, err := http.NewRequestWithContext(ctx, "POST", postBackUrl, bytes.NewReader(pathsBytes))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")

		// Send the postback
		log.Println("Sending", string(pathsBytes), "to", postBackUrl)
		client := &http.Client{}
		res, err := client.Do(req)
		if err != nil {
			return err
		}
		defer res.Body.Close()

		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("postback failed with status code: %d", res.StatusCode)
		}

		// Return the result
		return nil
	}

	// No command
	return fmt.Errorf("noop")
}
