// go build:windows
package interpreter

import (
	"context"
	"strings"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/hashicorp/go-hclog"
)

func NewPowershellExecutor() Executor {
	return NewBaseExecutor(
		"powershell",
		"\"$($PSVersionTable.PSVersion.Major).$($PSVersionTable.PSVersion.Minor)\"",
		true,
		func(command string) []string { return []string{"-Command", command} },
		func(path string) []string { return []string{"-File", path} },
		utils.NewFileSystem(),
	)
}

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

type defaultExecutor struct {
	PowershellExecutor Executor
	PwshExecutor       Executor
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

	return e.PowershellExecutor.Execute(ctx, message, device, logger, sys, domain)
}

func NewExecutor() Executor {
	return &defaultExecutor{
		PowershellExecutor: NewPowershellExecutor(),
		PwshExecutor:       NewPwshExecutor(),
	}
}
