//go:build windows

package main

import (
	"log"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func runUninstall(orgId string) {
	// Connect to service manager
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

}
