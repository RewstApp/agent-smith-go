package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/interpreter"
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
	// Create providers
	sys := agent.NewSystemInfoProvider()
	domain := agent.NewDomainInfoProvider()
	executor := interpreter.NewExecutor()
	fs := utils.NewFileSystem()

	uninstallContext, err := newUninstallContext(os.Args[1:])
	if err == nil {
		// Run uninstall routine
		runUninstall(uninstallContext)
		return
	}

	configContext, err := newConfigContext(os.Args[1:], sys, domain, fs)
	if err == nil {
		// Run config routine
		runConfig(configContext)
		return
	}

	serviceContext, err := newServiceContext(os.Args[1:], sys, domain, executor)
	if err == nil {
		// Run service routine
		runService(serviceContext)
		return
	}

	updateContext, err := newUpdateContext(os.Args[1:], sys, domain)
	if err == nil {
		// Run update routine
		runUpdate(updateContext)
		return
	}

	// Show usage
	loggingLevelsList := getAllowedConfigLevelsString("|")
	configFlagsList := fmt.Sprintf("[--logging-level %s] [--syslog] [--disable-agent-postback] [--no-auto-updates]", loggingLevelsList)
	usages := []string{
		"--uninstall",
		fmt.Sprintf("--config-url <CONFIG URL> --config-secret <CONFIG SECRET> %s", configFlagsList),
		"--config-file <CONFIG FILE> --log-file <LOG FILE>",
		fmt.Sprintf("--update %s", configFlagsList),
	}
	fmt.Printf("Usage: --org-id <ORG_ID> {%s}\n", strings.Join(usages, " | "))
	os.Exit(1)
}
