package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
)

type uninstallParams struct {
	OrgId     string
	Uninstall bool
}

func parseUninstallParams(args []string) (*uninstallParams, error) {
	var params uninstallParams

	fs := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	fs.StringVar(&params.OrgId, "org-id", "", "Organization ID")
	fs.BoolVar(&params.Uninstall, "uninstall", false, "Uninstall the agent")
	fs.SetOutput(bytes.NewBuffer([]byte{}))

	err := fs.Parse(args)
	if err != nil {
		return nil, err
	}

	if params.OrgId == "" {
		return nil, fmt.Errorf("missing org-id")
	}

	if !params.Uninstall {
		return nil, fmt.Errorf("missing uninstall")
	}

	return &params, nil
}

type configParams struct {
	OrgId        string
	ConfigUrl    string
	ConfigSecret string
}

func parseConfigParams(args []string) (*configParams, error) {
	var params configParams

	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.StringVar(&params.OrgId, "org-id", "", "Organization ID")
	fs.StringVar(&params.ConfigUrl, "config-url", "", "Configuration URL")
	fs.StringVar(&params.ConfigSecret, "config-secret", "", "Configuration Secret")
	fs.SetOutput(bytes.NewBuffer([]byte{}))

	err := fs.Parse(args)
	if err != nil {
		return nil, err
	}

	if params.OrgId == "" {
		return nil, fmt.Errorf("missing org-id")
	}

	if params.ConfigUrl == "" {
		return nil, fmt.Errorf("missing config-url")
	}

	if params.ConfigSecret == "" {
		return nil, fmt.Errorf("missing config-secret")
	}

	return &params, nil
}

type serviceParams struct {
	OrgId      string
	ConfigFile string
	LogFile    string
}

func parseServiceParams(args []string) (*serviceParams, error) {
	var params serviceParams

	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.StringVar(&params.OrgId, "org-id", "", "Organization ID")
	fs.StringVar(&params.ConfigFile, "config-file", "", "Configuration File")
	fs.StringVar(&params.LogFile, "log-file", "", "Log file")
	fs.SetOutput(bytes.NewBuffer([]byte{}))

	err := fs.Parse(args)
	if err != nil {
		return nil, err
	}

	if params.OrgId == "" {
		return nil, fmt.Errorf("missing org-id")
	}

	if params.ConfigFile == "" {
		return nil, fmt.Errorf("missing config-file")
	}

	if params.LogFile == "" {
		return nil, fmt.Errorf("missing log-file")
	}

	return &params, nil
}

func main() {
	uninstallParams, err := parseUninstallParams(os.Args[1:])
	if err == nil {
		// Run uninstall routine
		runUninstall(uninstallParams)
		return
	}

	configParams, err := parseConfigParams(os.Args[1:])
	if err == nil {
		// Run config routine
		runConfig(configParams)
		return
	}

	serviceParams, err := parseServiceParams(os.Args[1:])
	if err == nil {
		// Run service routine
		runService(serviceParams)
		return
	}

	// Show usage
	fmt.Println("Usage: --org-id <ORG_ID> {--uninstall | --config-url <CONFIG URL> --config-secret <CONFIG SECRET> | --config-file <CONFIG FILE> --log-file <LOG FILE>}")
	os.Exit(1)
}
