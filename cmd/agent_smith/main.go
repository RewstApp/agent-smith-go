package main

import (
	"flag"
	"log"
)

func main() {
	// Parse command-line arguments
	var orgId string
	var configUrl string
	var configSecret string
	var configFile string
	var logFile string
	var uninstallFlag bool

	flag.StringVar(&orgId, "org-id", "", "Organization ID")

	// Config routine arguments
	flag.StringVar(&configUrl, "config-url", "", "Configuration URL")
	flag.StringVar(&configSecret, "config-secret", "", "Config secret")

	// Service routine arguments
	flag.StringVar(&configFile, "config-file", "", "Config file")
	flag.StringVar(&logFile, "log-file", "", "Log file")

	// Uninstall routine flags
	flag.BoolVar(&uninstallFlag, "uninstall", false, "Uninstall the agent")

	flag.Parse()

	// Make sure that org id is specified
	if orgId == "" {
		log.Println("Missing org-id parameter")
		return
	}

	// Run uninstall routine
	if uninstallFlag {
		runUninstall(orgId)
		return
	}

	// Run config routine
	if configUrl != "" && configSecret != "" {
		runConfig(orgId, configUrl, configSecret)
		return
	}

	// Run service routine
	if configFile != "" && logFile != "" {
		runService(orgId, configFile, logFile)
		return
	}

	log.Println("Missing flag")
}
