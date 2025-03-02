package interpreter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/RewstApp/agent-smith-go/internal/agent"
)

type errorResult struct {
	Error string `json:"error"`
}

func errorResultBytes(err error) []byte {
	result := errorResult{err.Error()}

	bytes, err := json.MarshalIndent(&result, "", "  ")
	if err != nil {
		// Fallback
		return []byte(err.Error())
	}

	return bytes
}

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

func (msg *Message) Execute(ctx context.Context, device agent.Device) []byte {
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

		// Load the paths data
		var paths agent.PathsData
		err := paths.Load(ctx, device.RewstOrgId)
		if err != nil {
			return errorResultBytes(err)
		}

		// Convert to bytes in json
		pathsBytes, err := json.MarshalIndent(&paths, "", "  ")
		if err != nil {
			return errorResultBytes(err)
		}

		return pathsBytes
	}

	// No command
	return errorResultBytes(fmt.Errorf("noop"))
}

func (msg *Message) CreatePostbackRequest(ctx context.Context, device agent.Device, body io.Reader) (*http.Request, error) {
	// Create a postback url
	postBackUrl := fmt.Sprintf("https://%s/webhooks/custom/action/%s", device.RewstEngineHost, strings.ReplaceAll(msg.PostId, ":", "/"))

	// Create an http request
	req, err := http.NewRequestWithContext(ctx, "POST", postBackUrl, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	// Return the request
	return req, nil
}
