package interpreter

import (
	"context"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/hashicorp/go-hclog"
)

type Executor interface {
	Execute(ctx context.Context, message *Message, device agent.Device, logger hclog.Logger, sys agent.SystemInfoProvider, domain agent.DomainInfoProvider) []byte
}

type BuildExecuteCommandArgsFunc = func(command string) []string

type BuildExecuteFileArgsFunc = func(path string) []string
