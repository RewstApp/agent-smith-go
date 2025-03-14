//go:build windows

package main

import (
	"log"
	"os"
	"runtime"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/version"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const pollingInterval = time.Second

func runUninstall(params *uninstallParams) {
	// Show header
	log.Println("Agent Smith Version:", version.Version)
	log.Println("Running on:", runtime.GOOS)

	svcMgr, err := mgr.Connect()
	if err != nil {
		log.Println("Failed to connect to service manager:", err)
		return
	}
	defer svcMgr.Disconnect()

	name := agent.GetServiceName(params.OrgId)

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
		status, err = service.Control(svc.Stop)
		if err != nil {
			log.Println("Failed to send stop command:", err)
			return
		}

		log.Println("Sent stop command to", name)

		for {
			// Check if the service stopped
			if status.State == svc.Stopped {
				break
			}

			log.Println("Waiting for", name, "to stop...")
			time.Sleep(pollingInterval)

			status, err = service.Query()
			if err != nil {
				log.Println("Failed to query service status:", err)
				return
			}
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
	dataDir := agent.GetDataDirectory(params.OrgId)
	err = os.RemoveAll(dataDir)
	if err != nil {
		log.Println("Failed to delete directory:", dataDir)
		return
	}
	log.Println(dataDir, "deleted")

	// Delete program directory
	programDir := agent.GetProgramDirectory(params.OrgId)
	err = os.RemoveAll(programDir)
	if err != nil {
		log.Println("Failed to delete directory:", programDir)
		return
	}
	log.Println(programDir, "deleted")

	// Delete scripts directory
	scriptsDir := agent.GetScriptsDirectory(params.OrgId)
	err = os.RemoveAll(scriptsDir)
	if err != nil {
		log.Println("Failed to delete directory:", scriptsDir)
		return
	}
	log.Println(scriptsDir, "deleted")
}
