package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/RewstApp/agent-smith-go/internal/utils"
)

var defaultDirChmod os.FileMode = 0755

func getOrCreateProgramDirectory(orgId string) (string, error) {
	// Get program files directory
	programFilesDir := os.Getenv("PROGRAMFILES")

	// Build the program directory based on organization id
	dir := filepath.Join(programFilesDir, fmt.Sprintf("RewstRemoteAgent/%s", orgId))

	// Check it exists
	if !utils.DirExists(dir) {
		err := os.MkdirAll(dir, defaultDirChmod)
		if err != nil {
			return "", err
		}
	}

	return dir, nil
}

func getOrCreateDataDirectory(orgId string) (string, error) {
	// Get program data directory
	programDataDir := os.Getenv("PROGRAMDATA")

	// Build the program directory based on organization id
	dir := filepath.Join(programDataDir, fmt.Sprintf("RewstRemoteAgent/%s", orgId))

	// Check it exists
	if !utils.DirExists(dir) {
		err := os.MkdirAll(dir, defaultDirChmod)
		if err != nil {
			return "", err
		}
	}

	return dir, nil
}

func GetAgentExecutablePath(orgId string) (string, error) {
	// Get program directory
	dir, err := getOrCreateProgramDirectory(orgId)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, fmt.Sprintf("rewst_remote_agent_%s.win.exe", orgId)), nil
}

func GetServiceExecutablePath(orgId string) (string, error) {
	// Get program directory
	dir, err := getOrCreateProgramDirectory(orgId)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, fmt.Sprintf("rewst_windows_service_%s.win.exe", orgId)), nil
}

func GetServiceManagerPath(orgId string) (string, error) {
	// Get program directory
	dir, err := getOrCreateProgramDirectory(orgId)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, fmt.Sprintf("rewst_service_manager_%s.win.exe", orgId)), nil
}

func GetConfigFilePath(orgId string) (string, error) {
	// Get data directory
	dir, err := getOrCreateDataDirectory(orgId)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "config.json"), nil
}

func GetLogFilePath(orgId string) (string, error) {
	// Get data directory
	dir, err := getOrCreateDataDirectory(orgId)
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "rewst_agent.log"), nil
}

func GetScriptsDirectory(orgId string) (string, error) {
	// Get program files directory
	scriptsDir := os.Getenv("SYSTEMROOT")

	// Build the program directory based on organization id
	dir := filepath.Join(scriptsDir, fmt.Sprintf("../RewstRemoteAgent/scripts/%s", orgId))

	// Check it exists
	if !utils.DirExists(dir) {
		err := os.MkdirAll(dir, defaultDirChmod)
		if err != nil {
			return "", err
		}
	}

	return dir, nil
}

type PathsData struct {
	ServiceExecutablePath string   `json:"service_executable_path"`
	AgentExecutablePath   string   `json:"agent_executable_path"`
	ConfigFilePath        string   `json:"config_file_path"`
	ServiceManagerPath    string   `json:"service_manager_path"`
	Tags                  HostInfo `json:"tags"`
}

func (paths *PathsData) Load(ctx context.Context, orgId string) error {
	serviceExecutablePath, err := GetServiceExecutablePath(orgId)
	if err != nil {
		return err
	}
	paths.ServiceExecutablePath = serviceExecutablePath

	agentExecutablePath, err := GetAgentExecutablePath(orgId)
	if err != nil {
		return err
	}
	paths.AgentExecutablePath = agentExecutablePath

	configFilePath, err := GetConfigFilePath(orgId)
	if err != nil {
		return err
	}
	paths.ConfigFilePath = configFilePath

	serviceManagerPath, err := GetServiceManagerPath(orgId)
	if err != nil {
		return err
	}
	paths.ServiceManagerPath = serviceManagerPath

	return paths.Tags.Load(ctx, orgId)
}

func GetOrgIdFromExecutable() (string, error) {
	exec, err := os.Executable()
	if err != nil {
		log.Println("Executable name not found:", err)
		return "", err
	}

	filename := filepath.Base(exec)
	fragments := strings.Split(strings.Split(filename, ".")[0], "_")

	if len(fragments) != 4 {
		return "", fmt.Errorf("missing org id from executable: %s", filename)
	}

	return fragments[3], nil
}

func GetServiceName(orgId string) string {
	return fmt.Sprintf("RewstRemoteAgent_%s", orgId)
}
