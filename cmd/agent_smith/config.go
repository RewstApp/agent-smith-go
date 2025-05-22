package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
)

type fetchConfigurationResponse struct {
	Configuration agent.Device `json:"configuration"`
}

func runConfig(params *configParams) {
	// Show header
	log.Println("Agent Smith Version:", version.Version)
	log.Println("Running on:", runtime.GOOS)

	// Get installation paths data
	var pathsData agent.PathsData
	err := pathsData.Load(context.Background(), params.OrgId)
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
	log.Println("Sending", string(hostInfoBytes), "to", params.ConfigUrl)
	req, err := http.NewRequestWithContext(context.Background(), "POST", params.ConfigUrl, bytes.NewReader(hostInfoBytes))
	if err != nil {
		log.Println("Failed to create request:", err)
		return
	}
	req.Header.Set("x-rewst-secret", params.ConfigSecret)
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
	dataDir := agent.GetDataDirectory(params.OrgId)
	err = utils.CreateFolderIfMissing(dataDir)
	if err != nil {
		log.Println("Failed to create data directory:", err)
		return
	}

	// Save the configuration file
	configFilePath := agent.GetConfigFilePath(params.OrgId)
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

	name := agent.GetServiceName(params.OrgId)

	// Stop and delete the service if it already exists
	existingService, err := service.Open(name)
	if err == nil {
		if existingService.IsActive() {
			log.Println("Stopping service", name, "...")
			err = existingService.Stop()
			if err != nil {
				log.Println("Failed to stop service:", err)
				existingService.Close()
				return
			}
		}

		// Delete the service
		err = existingService.Delete()
		if err != nil {
			log.Println("Failed to delete service:", err)
			return
		}
		log.Println(name, "deleted")

		// Wait for some time for the service executable to clean up
		existingService.Close()
		log.Println("Waiting for service executable to stop...")
		time.Sleep(serviceExecutableTimeout)
	}

	log.Println("Configuration saved to", configFilePath)
	log.Println("Logs will be saved to", agent.GetLogFilePath(params.OrgId))

	// Create the program directory
	programDir := agent.GetProgramDirectory(params.OrgId)
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

	agentExecutablePath := agent.GetAgentExecutablePath(params.OrgId)
	err = os.WriteFile(agentExecutablePath, execFileBytes, utils.DefaultExecutableFileMod)
	if err != nil {
		log.Println("Failed to create agent executable:", err)
		return
	}

	log.Println("Agent installed to", agentExecutablePath)
	log.Println("Commands will be temporarily saved to", agent.GetScriptsDirectory(params.OrgId))

	// Create the service
	log.Println("Creating service", name, "...")

	svc, err := service.Create(service.AgentParams{
		Name:                name,
		AgentExecutablePath: agentExecutablePath,
		OrgId:               params.OrgId,
		ConfigFilePath:      configFilePath,
		LogFilePath:         agent.GetLogFilePath(params.OrgId),
	})
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
