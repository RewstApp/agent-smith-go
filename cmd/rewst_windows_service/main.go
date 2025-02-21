//go:build windows

package main

import (
	"context"
	"log"
	"os"
	"os/exec"

	"github.com/RewstApp/agent-smith-go/internal/agent"
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
	log.SetPrefix("[rewst_windows_service] ")

	// Check if the executable is running as a windows service
	isWinSvc, err := svc.IsWindowsService()
	if err != nil {
		log.Println("Failed to query execution status:", err)
		return
	}
	if !isWinSvc {
		log.Println("Executable should be run as a service")
		return
	}

	// Create service instance
	var service Service

	// Get organization id from executable name
	service.OrgId, err = agent.GetOrgIdFromExceutable()
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

	// Configure logging output
	service.LogFilePath, err = agent.GetLogFilePath(service.OrgId)
	if err != nil {
		log.Println("GetLogFilePath() failed:", err)
		return
	}

	logFile, err := os.OpenFile(service.LogFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Println("Failed to open log:", err)
		return
	}
	defer logFile.Close()
	log.SetOutput(logFile)

	// Start the windows service
	err = svc.Run(agent.GetServiceName(service.OrgId), &service)
	if err != nil {
		log.Println("Failed to run the service:", err)
		return
	}

	log.Println("Service closed successfully")
}
