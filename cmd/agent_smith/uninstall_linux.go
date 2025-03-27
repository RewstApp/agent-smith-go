//go:build linux

package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/version"
)

func runUninstall(params *uninstallParams) {
	// Show header
	log.Println("Agent Smith Version:", version.Version)
	log.Println("Running on:", runtime.GOOS)

	name := agent.GetServiceName(params.OrgId)

	cmd := exec.Command("systemctl", "is-active", name)
	err := cmd.Run()
	if err == nil {
		cmd = exec.Command("systemctl", "stop", name)
		err = cmd.Run()
		if err != nil {
			log.Println("Failed to stop service:", err)
			return
		}

		log.Println(name, "stopped")
	}

	// Delete the service
	cmd = exec.Command("systemctl", "disable", name)
	err = cmd.Run()
	if err != nil {
		log.Println("Failed to delete service:", err)
		return
	}

	serviceConfigFilePath := filepath.Join("/etc/systemd/system", fmt.Sprintf("%s.service", name))
	err = os.Remove(serviceConfigFilePath)
	if err != nil {
		log.Println("Failed to delete service:", err)
		return
	}

	cmd = exec.Command("systemctl", "daemon-reload")
	err = cmd.Run()
	if err != nil {
		log.Println("Failed to delete service:", err)
		return
	}
	log.Println(name, "deleted")

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
