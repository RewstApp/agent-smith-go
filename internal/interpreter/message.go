package interpreter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/hashicorp/go-hclog"
)

type Message struct {
	PostId              string      `json:"post_id"`
	Commands            string      `json:"commands"`
	InterpreterOverride StringFalse `json:"interpreter_override"`
	GetInstallation     bool        `json:"get_installation"`
	Type                string      `json:"type"`
	Content             string      `json:"content"`
}

func (msg *Message) Parse(data []byte) error {
	return json.Unmarshal(data, msg)
}

func (msg *Message) Execute(
	executor Executor,
	ctx context.Context,
	device agent.Device,
	logger hclog.Logger,
	sys agent.SystemInfoProvider,
	domain agent.DomainInfoProvider,
) []byte {
	// Execute commands if given
	if msg.Commands != "" {
		logger.Info("Executing commands", "interpreter_override", msg.InterpreterOverride.Value)
		return executor.Execute(ctx, msg, device, logger, sys, domain)
	}

	// Get installation data if given
	if msg.GetInstallation {
		logger.Info("Executing get_installation...")

		// Load the paths data
		paths, err := agent.NewPathsData(ctx, device.RewstOrgId, logger, sys, domain)
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

func (msg *Message) CreatePostbackRequest(
	ctx context.Context,
	device agent.Device,
	body io.Reader,
) (*http.Request, error) {
	// Create a postback url
	postBackUrl := fmt.Sprintf(
		"https://%s/webhooks/custom/action/%s",
		device.RewstEngineHost,
		strings.ReplaceAll(msg.PostId, ":", "/"),
	)

	// Create an http request
	req, err := utils.NewRequestWithContext(ctx, "POST", postBackUrl, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	// Return the request
	return req, nil
}
