//go:build windows

package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"golang.org/x/sys/windows/svc"
)

type Service struct{}

func (s Service) Execute(args []string, request <-chan svc.ChangeRequest, response chan<- svc.Status) (bool, uint32) {
	response <- svc.Status{State: svc.StartPending}

	return true, 0
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

	// Get the base directory of the executable
	dir, err := utils.BaseDirectory()
	if err != nil {
		log.Println("Failed to get base directory:", err)
		return
	}

	// Configure logging output
	logFile, err := os.OpenFile(filepath.Join(dir, utils.LogFileName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Println("Failed to open log:", err)
		return
	}
	defer logFile.Close()
	log.SetOutput(logFile)

	// Load the configuration
	conf := agent.Device{}
	err = conf.Load(filepath.Join(dir, "config.json"))
	if err != nil {
		log.Println("Failed to load config file:", err)
		return
	}
	log.Println("Configuration file loaded")

	// Start the windows service
	err = svc.Run("AgentSmithGoService", &Service{})
	if err != nil {
		log.Println("Failed to run the service:", err)
		return
	}

	log.Println("Service closed successfully")
}
