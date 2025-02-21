//go:build windows

package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"golang.org/x/sys/windows/svc"
)

type Service struct {
	OrgId string
}

func (service *Service) Execute(args []string, request <-chan svc.ChangeRequest, response chan<- svc.Status) (bool, uint32) {
	response <- svc.Status{State: svc.StartPending}

	// Get paths
	agentExecutablePath, err := agent.GetAgentExecutablePath(service.OrgId)
	if err != nil {
		log.Println("Failed GetAgentExecutablePath():", err)
		response <- svc.Status{State: svc.Stopped}
		return false, 1
	}

	configFilePath, err := agent.GetConfigFilePath(service.OrgId)
	if err != nil {
		log.Println("Failed GetConfigFilePath():", err)
		response <- svc.Status{State: svc.Stopped}
		return false, 1
	}

	logFilePath, err := agent.GetLogFilePath(service.OrgId)
	if err != nil {
		log.Println("Failed GetLogFilePath():", err)
		response <- svc.Status{State: svc.Stopped}
		return false, 1
	}

	// Create a context to cancel the command
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, agentExecutablePath, "--config", configFilePath, "--log", logFilePath)

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

	// Get organization id from executable name
	orgId, err := agent.GetOrgIdFromExceutable()
	if err != nil {
		log.Println("Executable name not found:", err)
		return
	}

	// Configure logging output
	logFilePath, err := agent.GetLogFilePath(orgId)
	if err != nil {
		log.Println("Failed to get log file path:", err)
		return
	}

	logFile, err := os.OpenFile(logFilePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Println("Failed to open log:", err)
		return
	}
	defer logFile.Close()
	log.SetOutput(logFile)

	// Start the windows service
	name := fmt.Sprintf("RewstRemoteAgent_%s", orgId)
	err = svc.Run(name, &Service{OrgId: orgId})
	if err != nil {
		log.Println("Failed to run the service:", err)
		return
	}

	log.Println("Service closed successfully")
}
