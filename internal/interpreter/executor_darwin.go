// go build:darwin
package interpreter

import (
	"context"
	"strings"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/hashicorp/go-hclog"
)

func NewPwshExecutor() Executor {
	return NewBaseExecutor(
		"pwsh",
		"\"$($PSVersionTable.PSVersion.Major).$($PSVersionTable.PSVersion.Minor)\"",
		true,
		func(command string) []string { return []string{"-Command", command} },
		func(path string) []string { return []string{"-File", path} },
		utils.NewFileSystem(),
	)
}

func NewBashExecutor() Executor {
	return NewBaseExecutor(
		"bash",
		"echo \"$BASH_VERSION\"",
		false,
		func(command string) []string { return []string{"-c", command} },
		func(path string) []string { return []string{path} },
		utils.NewFileSystem(),
	)
}

type defaultExecutor struct {
	BashExecutor Executor
	PwshExecutor Executor
}

func (e *defaultExecutor) Execute(
	ctx context.Context,
	message *Message,
	device agent.Device,
	logger hclog.Logger,
	sys agent.SystemInfoProvider,
	domain agent.DomainInfoProvider,
) []byte {
	shell := strings.ToLower(message.InterpreterOverride.Value)
	if shell == "pwsh" {
		return e.PwshExecutor.Execute(ctx, message, device, logger, sys, domain)
	}

	return e.BashExecutor.Execute(ctx, message, device, logger, sys, domain)
}

func NewExecutor() Executor {
	return &defaultExecutor{
		BashExecutor: NewBashExecutor(),
		PwshExecutor: NewPwshExecutor(),
	}
}
