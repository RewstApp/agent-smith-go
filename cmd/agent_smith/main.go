package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/RewstApp/agent-smith-go/internal/utils"
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
	LoggingLevel string
}

var allowedLoggingLevels = map[string]bool{
	string(utils.Info):    true,
	string(utils.Warn):    true,
	string(utils.Error):   true,
	string(utils.Off):     true,
	string(utils.Default): true,
}

func parseConfigParams(args []string) (*configParams, error) {
	var params configParams

	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.StringVar(&params.OrgId, "org-id", "", "Organization ID")
	fs.StringVar(&params.ConfigUrl, "config-url", "", "Configuration URL")
	fs.StringVar(&params.ConfigSecret, "config-secret", "", "Configuration Secret")
	fs.StringVar(&params.LoggingLevel, "logging-level", string(utils.Default), "Logging level: info, warn, error")
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

	if !allowedLoggingLevels[params.LoggingLevel] {
		return nil, fmt.Errorf("invalid logging-level")
	}

	return &params, nil
}

func getAllowedConfigLevelsString() string {
	var levels []string
	for level := range allowedLoggingLevels {
		// Skip default
		if level == string(utils.Default) {
			continue
		}

		levels = append(levels, level)
	}

	return strings.Join(levels, "|")
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
	fmt.Printf("Usage: --org-id <ORG_ID> {--uninstall | --config-url <CONFIG URL> --config-secret <CONFIG SECRET> [--logging-level [%s]] | --config-file <CONFIG FILE> --log-file <LOG FILE>}\n", getAllowedConfigLevelsString())
	os.Exit(1)
}
