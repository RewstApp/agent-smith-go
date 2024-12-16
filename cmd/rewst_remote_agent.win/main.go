package main

import (
	"log"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
)

type myService struct{}

// Execute is the main entry point for your service logic
func (m *myService) Execute(args []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {

	// Notify Windows that the service is starting
	status <- svc.Status{State: svc.StartPending}

	// Indicate the service is running
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}

	log.Println("Service is running...")

	// Main service loop
	for {
		select {
		case r := <-req:
			switch r.Cmd {
			case svc.Stop, svc.Shutdown:
				log.Println("Service is stopping...")
				status <- svc.Status{State: svc.StopPending}
				return true, 0
			}
		default:
			// Your service logic (e.g., background tasks) goes here
			log.Println("Service is working...")
			time.Sleep(5 * time.Second) // Simulate work
		}
	}
}

func main() {
	// Get the path of the current executable
	exePath, err := os.Executable()
	if err != nil {
		log.Println("Error:", err)
		return
	}
	dir := filepath.Dir(exePath)

	// Setup the log file
	logFile, err := os.OpenFile(dir+"\\rewst.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		log.Fatal(err)
		return
	}
	defer logFile.Close()
	log.SetOutput(logFile)

	// Check if the window service is run
	isWindowsService, err := svc.IsWindowsService()
	if err != nil {
		log.Fatalf("Failed to determine session type: %v", err)
	}

	if isWindowsService {
		// Run as a Windows service
		log.Println("Running Windows service")
		svc.Run("AgentSmithGoService", &myService{})
	} else {
		// Run as a console application
		log.Println("Running interactively. This is not a Windows service.")
	}
}
