//go:build windows

package main

import (
	"log"
	"os"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func runUninstall(orgId string) {
	svcMgr, err := mgr.Connect()
	if err != nil {
		log.Println("Failed to connect to service manager:", err)
		return
	}
	defer svcMgr.Disconnect()

	name := agent.GetServiceName(orgId)

	service, err := svcMgr.OpenService(name)
	if err != nil {
		log.Println("Failed to open service:", name)
		return
	}
	defer service.Close()

	status, err := service.Query()
	if err != nil {
		log.Println("Failed to query service status:", status)
		return
	}

	if status.State == svc.Running {
		// Stop the service is running
		service.Control(svc.Stop)
		log.Println(name, "is stopping")

		// Wait a bit to see if it stopped
		stopped := false
		for range 10 {
			// Wait for a second
			time.Sleep(time.Second)

			// Get the status
			status, err := service.Query()
			if err != nil {
				log.Println("Failed to query service status:", status)
				return
			}

			// Check if stopped
			if status.State == svc.Stopped {
				stopped = true
				break
			}
		}

		if !stopped {
			log.Println(name, "didn't stop within the time")
			return
		}

		log.Println(name, "stopped")
	}

	// Delete the service
	err = service.Delete()
	if err != nil {
		log.Println("Failed to delete service:", err)
		return
	}
	log.Println(name, "deleted")

	// Delete data directory
	dataDir := agent.GetDataDirectory(orgId)
	err = os.RemoveAll(dataDir)
	if err != nil {
		log.Println("Failed to delete directory:", dataDir)
		return
	}
	log.Println(dataDir, "deleted")

	// Delete program directory
	programDir := agent.GetProgramDirectory(orgId)
	err = os.RemoveAll(programDir)
	if err != nil {
		log.Println("Failed to delete directory:", programDir)
		return
	}
	log.Println(programDir, "deleted")

	// Delete scripts directory
	scriptsDir := agent.GetScriptsDirectory(orgId)
	err = os.RemoveAll(scriptsDir)
	if err != nil {
		log.Println("Failed to delete directory:", scriptsDir)
		return
	}
	log.Println(scriptsDir, "deleted")
}
