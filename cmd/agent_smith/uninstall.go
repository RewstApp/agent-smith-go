package main

import (
	"log"
	"os"
	"runtime"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/version"
)

const serviceExecutableTimeout = time.Second * 5

func runUninstall(params *uninstallParams) {
	// Show header
	log.Println("Agent Smith Version:", version.Version)
	log.Println("Running on:", runtime.GOOS)

	name := agent.GetServiceName(params.OrgId)

	service, err := service.Open(name)
	if err != nil {
		log.Println("Failed to open service", name, ":", err)
		return
	}
	defer service.Close()

	if service.IsActive() {
		log.Println("Stopping service", name, "...")
		err = service.Stop()
		if err != nil {
			log.Println("Failed to stop service:", err)
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

	// Wait for some time for the service executable to clean up
	log.Println("Waiting for service executable to stop...")
	time.Sleep(serviceExecutableTimeout)

	// Delete data directory
	dataDir := agent.GetDataDirectory(params.OrgId)
	err = os.RemoveAll(dataDir)
	if err != nil {
		log.Println("Failed to delete directory", dataDir, ":", err)
		return
	}
	log.Println(dataDir, "deleted")

	// Delete program directory
	programDir := agent.GetProgramDirectory(params.OrgId)
	err = os.RemoveAll(programDir)
	if err != nil {
		log.Println("Failed to delete directory", programDir, ":", err)
		return
	}
	log.Println(programDir, "deleted")

	// Delete scripts directory
	scriptsDir := agent.GetScriptsDirectory(params.OrgId)
	err = os.RemoveAll(scriptsDir)
	if err != nil {
		log.Println("Failed to delete directory", scriptsDir, ":", err)
		return
	}
	log.Println(scriptsDir, "deleted")
}
