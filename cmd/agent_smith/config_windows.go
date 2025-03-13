//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
	"golang.org/x/sys/windows/svc/mgr"
)

type fetchConfigurationResponse struct {
	Configuration agent.Device `json:"configuration"`
}

func runConfig(orgId string, configUrl string, configSecret string) {
	// Show header
	log.Println("Agent Smith Version:", version.Version)
	log.Println("Running on:", runtime.GOOS)

	// Get installation paths data
	var pathsData agent.PathsData
	err := pathsData.Load(context.Background(), orgId)
	if err != nil {
		log.Println("Failed to read paths:", err)
		return
	}

	// Fetch configuration
	hostInfoBytes, err := json.MarshalIndent(pathsData.Tags, "", "  ")
	if err != nil {
		log.Println("Failed to read host info:", err)
		return
	}

	// Prepare http request and send
	log.Println("Sending", string(hostInfoBytes), "to", configUrl)
	req, err := http.NewRequestWithContext(context.Background(), "POST", configUrl, bytes.NewReader(hostInfoBytes))
	if err != nil {
		log.Println("Failed to create request:", err)
		return
	}
	req.Header.Set("x-rewst-secret", configSecret)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	res, err := client.Do(req)
	if err != nil {
		log.Println(err)
		return
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		log.Println("Failed to fetch configuration with status code:", res.StatusCode)
		return
	}
	log.Println("Sent with response status code", res.StatusCode)

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		log.Println("Failed to read response:", err)
		return
	}

	// Parse the fetch configuration response
	var response fetchConfigurationResponse
	err = json.Unmarshal(bodyBytes, &response)
	if err != nil {
		log.Println("Failed to parse response:", err)
		return
	}

	// Create the data directory
	dataDir := agent.GetDataDirectory(orgId)
	err = utils.CreateFolderIfMissing(dataDir)
	if err != nil {
		log.Println("Failed to create data directory:", err)
		return
	}

	// Save the configuration file
	configFilePath := agent.GetConfigFilePath(orgId)
	configBytes, err := json.MarshalIndent(response.Configuration, "", "  ")
	if err != nil {
		log.Println("Failed to print config file:", err)
		return
	}

	// Got configuration
	log.Println("Received configuration:", string(configBytes))

	err = os.WriteFile(configFilePath, configBytes, utils.DefaultFileMod)
	if err != nil {
		log.Println("Failed to save config:", err)
		return
	}

	log.Println("Configuration saved to", configFilePath)
	log.Println("Logs will be saved to", agent.GetLogFilePath(orgId))

	// Create the program directory
	programDir := agent.GetProgramDirectory(orgId)
	err = utils.CreateFolderIfMissing(programDir)
	if err != nil {
		log.Println("Failed to create program directory:", err)
		return
	}

	// Copy the agent executable
	execFilePath, err := os.Executable()
	if err != nil {
		log.Println("Failed to get executable:", err)
		return
	}

	execFileBytes, err := os.ReadFile(execFilePath)
	if err != nil {
		log.Println("Failed to read executable file:", err)
		return
	}

	agentExecutablePath := agent.GetAgentExecutablePath(orgId)
	err = os.WriteFile(agentExecutablePath, execFileBytes, utils.DefaultFileMod)
	if err != nil {
		log.Println("Failed to create agent executable:", err)
		return
	}

	log.Println("Agent installed to", agentExecutablePath)
	log.Println("Commands will be temporarily saved to", agent.GetScriptsDirectory(orgId))

	// Create the service
	svcMgr, err := mgr.Connect()
	if err != nil {
		log.Println("Failed to connect service manager:", err)
		return
	}
	defer svcMgr.Disconnect()

	name := agent.GetServiceName(orgId)
	log.Println("Creating service", name, "...")

	svc, err := svcMgr.CreateService(name, agentExecutablePath, mgr.Config{
		StartType:        mgr.StartAutomatic,
		Description:      fmt.Sprintf("Rewst Remote Agent for Org %s", orgId),
		DelayedAutoStart: true,
	}, "--org-id", orgId, "--config-file", configFilePath, "--log-file", agent.GetLogFilePath(orgId))
	if err != nil {
		log.Println("Failed to create service:", err)
		return
	}
	defer svc.Close()
	log.Println("Service created")

	// Start the service
	log.Println("Starting service", name, "...")
	err = svc.Start()
	if err != nil {
		log.Println("Failed to start service:", err)
		return
	}

	log.Println("Service started")
}
