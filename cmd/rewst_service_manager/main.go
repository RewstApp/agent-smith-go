package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/agent"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/RewstApp/agent-smith-go/internal/version"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func stateToString(state svc.State) string {
	switch state {
	case svc.Stopped:
		return "STOPPED"
	case svc.StartPending:
		return "PENDING"
	case svc.StopPending:
		return "STOP PENDING"
	case svc.Running:
		return "RUNNING"
	case svc.ContinuePending:
		return "CONTINUE PENDING"
	case svc.PausePending:
		return "PAUSE PENDING"
	case svc.Paused:
		return "PAUSED"
	default:
		return ""
	}
}

func startService(svcMgr *mgr.Mgr, name string) error {
	service, err := svcMgr.OpenService(name)
	if err != nil {
		return err
	}
	defer service.Close()

	err = service.Start()
	if err != nil {
		return err
	}

	return nil
}

func stopService(svcMgr *mgr.Mgr, name string) error {
	service, err := svcMgr.OpenService(name)
	if err != nil {
		return err
	}
	defer service.Close()

	_, err = service.Control(svc.Stop)
	if err != nil {
		return err
	}

	for {
		status, err := service.Query()
		if err != nil {
			return err
		}

		if status.State == svc.Stopped {
			return nil
		}

		time.Sleep(time.Second)
	}
}

func main() {
	// Show header
	utils.ConfigureLogger("rewst_service_manager", os.Stdout)
	log.Println("Version:", version.Version)
	log.Println("Running on:", runtime.GOOS)

	// Parse command-line arguments
	var orgId string
	var install bool
	var uninstall bool
	var status bool
	var start bool
	var stop bool
	var restart bool

	flag.StringVar(&orgId, "org-id", "", "Organization ID")
	flag.BoolVar(&install, "install", false, "Install the service")
	flag.BoolVar(&uninstall, "uninstall", false, "Uninstall the service")
	flag.BoolVar(&status, "status", false, "Check the service status")
	flag.BoolVar(&start, "start", false, "Start the service")
	flag.BoolVar(&stop, "stop", false, "Stop the service")
	flag.BoolVar(&restart, "restart", false, "Restart the service")
	flag.Parse()

	// Configure logger
	utils.ConfigureLogger("rewst_service_manager", os.Stdout)

	// Validate arguments
	if len(orgId) == 0 {
		log.Println("Missing org-id parameter")
		return
	}

	// Connect to the service manager
	svcMgr, err := mgr.Connect()
	if err != nil {
		log.Println("Failed to connect to service manager:", err)
		return
	}
	defer svcMgr.Disconnect()

	name := agent.GetServiceName(orgId)

	// Execute based on the selected flag
	if install {
		serviceExecutablePath, err := agent.GetServiceExecutablePath(orgId)
		if err != nil {
			log.Println("GetServiceExecutablePath() failed:", err)
			return
		}

		service, err := svcMgr.CreateService(name, serviceExecutablePath, mgr.Config{
			DisplayName: fmt.Sprintf("Rewst Agent Service for Org %s", orgId),
			StartType:   mgr.StartAutomatic,
			Description: fmt.Sprintf("Rewst Agent Service for Org %s", orgId),
		})
		if err != nil {
			log.Println("CreateService() failed:", err)
			return
		}
		defer service.Close()

		service.Start()

		log.Println("Service", name, "created and started successfully!")
		return
	}

	if uninstall {
		service, err := svcMgr.OpenService(name)
		if err != nil {
			log.Println("Failed to open service:", name)
			return
		}
		defer service.Close()

		err = service.Delete()
		if err != nil {
			log.Println("Service failed to delete:", err)
			return
		}

		log.Println("Service", name, "deleted successfully!")
		return
	}

	if status {
		service, err := svcMgr.OpenService(name)
		if err != nil {
			log.Println("Failed to open service:", name)
			return
		}
		defer service.Close()

		status, err := service.Query()
		if err != nil {
			log.Println("Failed to query service status:", err)
			return
		}

		log.Println("Service", name, ":", stateToString(status.State))
		return
	}

	if start {
		log.Println("Starting service...")

		err = startService(svcMgr, name)
		if err != nil {
			log.Println("Failed to start service:", err)
			return
		}

		log.Println("Service", name, "started")
		return
	}

	if stop {
		log.Println("Stopping service...")

		err = stopService(svcMgr, name)
		if err != nil {
			log.Println("Failed to stop service:", err)
			return
		}

		log.Println("Service", name, "stopped")
		return
	}

	if restart {
		log.Println("Restarting service...")

		err = stopService(svcMgr, name)
		if err != nil {
			log.Println("Failed to stop service:", err)
			return
		}

		err = startService(svcMgr, name)
		if err != nil {
			log.Println("Failed to start service:", err)
			return
		}

		log.Println("Service", name, "restarted")
		return
	}

	log.Println("NOOP")
}
