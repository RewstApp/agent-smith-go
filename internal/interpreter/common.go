package interpreter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/hashicorp/go-hclog"
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

type result struct {
	Error  string `json:"error"`
	Output string `json:"output"`
}

func resultBytes(result *result) []byte {
	resultBytes, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return errorResultBytes(err)
	}

	return resultBytes
}

type StringFalse struct {
	Value string
}

func (sf *StringFalse) UnmarshalJSON(data []byte) error {
	// Try to parse bool
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		if b {
			sf.Value = "true"
		} else {
			sf.Value = ""
		}
		return nil
	}

	// Try to parse string
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		sf.Value = s
		return nil
	}

	return fmt.Errorf("unsupported type: %s", string(data))
}

type Message struct {
	PostId              string      `json:"post_id"`
	Commands            string      `json:"commands"`
	InterpreterOverride StringFalse `json:"interpreter_override"`
	GetInstallation     bool        `json:"get_installation"`
	Type                string      `json:"type"`
	Content             string      `json:"content"`
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

func (msg *Message) Execute(ctx context.Context, device agent.Device, logger hclog.Logger) []byte {
	// Execute commands if given
	if msg.Commands != "" {
		logger.Info("Executing commands...")

		// Select the correct interpreter
		switch msg.InterpreterOverride.Value {
		case "pwsh":
		case "powershell":
			return executeUsingPowershell(ctx, msg, device, logger)
		case "bash":
			return executeUsingBash(ctx, msg, device, logger)
		default:
			if runtime.GOOS == "windows" {
				return executeUsingPowershell(ctx, msg, device, logger)
			} else {
				return executeUsingBash(ctx, msg, device, logger)
			}
		}
	}

	// Get installation data if given
	if msg.GetInstallation {
		logger.Info("Executing get_installation...")

		// Load the paths data
		var paths agent.PathsData
		err := paths.Load(ctx, device.RewstOrgId, logger)
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
