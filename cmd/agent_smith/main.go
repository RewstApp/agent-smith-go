package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/interpreter"
	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/utils"
)

var allowedLoggingLevels = map[string]bool{
	string(utils.Info):    true,
	string(utils.Warn):    true,
	string(utils.Error):   true,
	string(utils.Off):     true,
	string(utils.Debug):   true,
	string(utils.Default): true,
}

func getAllowedConfigLevelsString(separator string) string {
	var levels []string
	for level := range allowedLoggingLevels {
		// Skip default
		if level == string(utils.Default) {
			continue
		}

		levels = append(levels, level)
	}

	return strings.Join(levels, separator)
}

func main() {
	// Validate platform-specific installation environment before any mode
	// resolves installation paths. On Windows this surfaces missing
	// PROGRAMFILES / PROGRAMDATA / SYSTEMDRIVE early instead of producing
	// malformed paths like `\RewstRemoteAgent\<orgId>`.
	if err := agent.ValidateInstallationEnvironment(); err != nil {
		fmt.Fprintf(os.Stderr, "environment error: %v\n", err)
		os.Exit(1)
	}

	// Create providers
	sys := agent.NewSystemInfoProvider()
	domain := agent.NewDomainInfoProvider()
	executor := interpreter.NewExecutor()
	fs := utils.NewFileSystem()
	svcMgr := service.NewServiceManager()

	// Attempt each operational mode in dispatch order. The first whose context
	// constructor succeeds wins. Each mode's validation error is retained so a
	// failed invocation can surface the specific reason for the mode the
	// operator most likely intended (see reportUsage).
	modeErrs := map[string]error{}

	diagnosticCtx, err := newDiagnosticContext(os.Args[1:], sys, domain, svcMgr, fs)
	modeErrs["diagnostic"] = err
	if err == nil {
		// Run diagnostic routine
		runDiagnostic(diagnosticCtx)
		return
	}

	uninstallContext, err := newUninstallContext(os.Args[1:], svcMgr, fs)
	modeErrs["uninstall"] = err
	if err == nil {
		// Run uninstall routine
		runUninstall(uninstallContext)
		return
	}

	configContext, err := newConfigContext(os.Args[1:], sys, domain, fs, svcMgr)
	modeErrs["config"] = err
	if err == nil {
		// Run config routine
		if err := runConfig(configContext); err != nil {
			fmt.Fprintf(os.Stderr, "config error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	serviceContext, err := newServiceContext(os.Args[1:], sys, domain, executor)
	modeErrs["service"] = err
	if err == nil {
		// Run service routine
		runService(serviceContext)
		return
	}

	updateContext, err := newUpdateContext(os.Args[1:], sys, domain, svcMgr, fs)
	modeErrs["update"] = err
	if err == nil {
		// Run update routine
		runUpdate(updateContext)
		return
	}

	// No mode matched: surface help, the relevant mode's validation error, or
	// the full multi-mode usage as appropriate.
	os.Exit(reportUsage(os.Args[1:], modeErrs, os.Stdout, os.Stderr))
}
