package interpreter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

func (msg *Message) Execute(ctx context.Context, device *agent.Device) (*Result, error) {
	// Execute commands if given
	if msg.Commands != nil {
		// Select the correct interpreter
		switch msg.InterpreterOverride {
		// TODO: Support other interpreter
		default:
			return executeUsingPowershell(ctx, msg, device)
		}
	}

	// Get installation data if given
	if msg.GetInstallation != nil && *msg.GetInstallation {
		// Create a postback url
		postBackUrl := fmt.Sprintf("https://%s/webhooks/custom/action/%s", device.RewstEngineHost, strings.ReplaceAll(msg.PostId, ":", "/"))

		// Load the paths data
		var paths agent.PathsData
		err := paths.Load(ctx, device.RewstOrgId)
		if err != nil {
			return nil, err
		}

		// Convert to bytes in json
		pathsBytes, err := json.Marshal(&paths)
		if err != nil {
			return nil, err
		}

		// Create an http request
		req, err := http.NewRequestWithContext(ctx, "POST", postBackUrl, bytes.NewReader(pathsBytes))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		// Send the postback
		client := &http.Client{}
		res, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer res.Body.Close()

		if res.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("postback failed with status code: %d", res.StatusCode)
		}

		// Return the result
		return &Result{
			PostId:   msg.PostId,
			Commands: nil,
			GetInstallation: &GetInstallationResult{
				StatusCode: res.StatusCode,
			},
		}, nil
	}

	// No command
	return nil, fmt.Errorf("noop")
}
