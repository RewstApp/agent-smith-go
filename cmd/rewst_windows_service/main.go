//go:build windows

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"golang.org/x/sys/windows/svc"
)

type Service struct {
	OrgId               string
	AgentExecutablePath string
	ConfigFilePath      string
	LogFilePath         string
}

func (service *Service) Execute(args []string, request <-chan svc.ChangeRequest, response chan<- svc.Status) (bool, uint32) {
	log.Println("Starting service...")
	response <- svc.Status{State: svc.StartPending}

	// Create a context to cancel the command
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, service.AgentExecutablePath, "--config", service.ConfigFilePath, "--log", service.LogFilePath)

	// Start the remote agent executable and notify
	cmd.Start()
	response <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	// Monitor when the executable stops on its own
	stopped := make(chan struct{})
	go func() {
		// Wait for the executable to finish
		cmd.Wait()

		// Trigger the stopped channel
		stopped <- struct{}{}
	}()

	for {
		select {
		case change := <-request:
			switch change.Cmd {
			case svc.Stop, svc.Shutdown:
				// Cancel the executable and update the status
				cancel()
				response <- svc.Status{State: svc.StopPending}
			}
		case <-stopped:
			response <- svc.Status{State: svc.Stopped}
			cancel()
			return true, 0
		}
	}
}

func main() {
	// Configure logger
	utils.ConfigureLogger("[rewst_windows_service]", os.Stdout)

	// Create service instance
	var service Service
	var err error

	// Get organization id from executable name
	service.OrgId, err = agent.GetOrgIdFromExecutable()
	if err != nil {
		log.Println("GetOrgIdFromExceutable() failed:", err)
		return
	}

	// Get paths
	service.AgentExecutablePath, err = agent.GetAgentExecutablePath(service.OrgId)
	if err != nil {
		log.Println("GetAgentExecutablePath() failed:", err)
		return
	}

	service.ConfigFilePath, err = agent.GetConfigFilePath(service.OrgId)
	if err != nil {
		log.Println("GetConfigFilePath() failed:", err)
		return
	}

	serviceManagerPath, err := agent.GetServiceManagerPath(service.OrgId)
	if err != nil {
		log.Println("GetServiceManagerPath() failed:", err)
		return
	}

	// Check if the executable is running as a windows service
	isWinSvc, err := svc.IsWindowsService()
	if err != nil {
		log.Println("Failed to query execution status:", err)
		return
	}
	if !isWinSvc {
		if len(os.Args) == 2 {
			// Check if we start the service
			// This is used to align with the installation script
			cmd := exec.Command(serviceManagerPath, "--org-id", service.OrgId, fmt.Sprintf("--%s", os.Args[1]))
			cmd.Stdout = os.Stdout
			cmd.Stdin = os.Stdin
			err = cmd.Run()
			if err != nil {
				log.Println("Failed to start service:", err)
				return
			}

			return
		}

		log.Println("Executable should be run as a service")
		return
	}

	service.LogFilePath, err = agent.GetLogFilePath(service.OrgId)
	if err != nil {
		log.Println("GetLogFilePath() failed:", err)
		return
	}

	// Configure logger
	logFile, err := os.OpenFile(service.LogFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Println("Failed to open log:", err)
		return
	}
	defer logFile.Close()

	// Configure logger with the new log file
	utils.ConfigureLogger("[rewst_windows_service]", logFile)

	// Start the windows service
	err = svc.Run(agent.GetServiceName(service.OrgId), &service)
	if err != nil {
		log.Println("Failed to run the service:", err)
		return
	}

	log.Println("Service closed successfully")
}
