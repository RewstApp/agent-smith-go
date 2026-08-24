package agent

import (
	"context"
	"path/filepath"

	"github.com/hashicorp/go-hclog"
)

// scriptsDirOverride replaces GetScriptsDirectory's computed path when
// non-nil. It exists only for tests that need script execution to land in a
// real, writable scratch directory rather than the real installation path,
// which EnsureSecureDir hardens to a privileged, agent-owned 0700 directory
// that an unprivileged test run cannot create. See
// SetScriptsDirectoryOverrideForTesting; nothing in a running agent ever
// calls it, so production always resolves through the path below.
var scriptsDirOverride func(orgId string) string

// GetScriptsDirectory returns the directory command scripts are written to
// before execution: a subdirectory of the org's agent-owned data directory,
// matching GetUpdatesDirectory's precedent (see internal/agent/updater.go).
//
// This used to be a subdirectory of the shared, world-writable system temp
// directory (os.TempDir()), which let any local unprivileged user pre-create
// it — or wait for a tmpfs /tmp to reset on reboot — with permissive
// ownership and mode before the agent ever ran, and reuse it undetected since
// directory creation is a no-op when the directory already exists. Landing
// under the data directory instead means the directory the agent writes
// scripts into, and executes them from, is one only the agent's own
// privileged account can create in the first place. See EnsureSecureDir,
// which additionally re-asserts ownership and mode on every use rather than
// trusting a pre-existing directory, and the README's "Hardening the Command
// Scripts Directory" section.
func GetScriptsDirectory(orgId string) string {
	if scriptsDirOverride != nil {
		return scriptsDirOverride(orgId)
	}
	return filepath.Join(GetDataDirectory(orgId), "scripts")
}

// SetScriptsDirectoryOverrideForTesting redirects GetScriptsDirectory to fn
// for the remaining lifetime of the process; passing nil restores the
// production path. It is exported so tests outside this package (the
// interpreter package's own tests, and cmd/agent_smith tests that exercise
// the real startup sweep) can point script execution at a t.TempDir() instead
// of the real, privileged installation path.
func SetScriptsDirectoryOverrideForTesting(fn func(orgId string) string) {
	scriptsDirOverride = fn
}

type PathsData struct {
	ServiceExecutablePath string    `json:"service_executable_path"`
	AgentExecutablePath   string    `json:"agent_executable_path"`
	ConfigFilePath        string    `json:"config_file_path"`
	ServiceManagerPath    string    `json:"service_manager_path"`
	Tags                  *HostInfo `json:"tags"`
}

func NewPathsData(
	ctx context.Context,
	orgId string,
	logger hclog.Logger,
	sys SystemInfoProvider,
	domain DomainInfoProvider,
) (*PathsData, error) {
	var paths PathsData

	paths.ServiceExecutablePath = GetServiceExecutablePath(orgId)
	paths.AgentExecutablePath = GetAgentExecutablePath(orgId)
	paths.ConfigFilePath = GetConfigFilePath(orgId)
	paths.ServiceManagerPath = GetServiceManagerPath(orgId)

	hostInfo, err := NewHostInfo(ctx, orgId, logger, sys, domain)
	if err != nil {
		return nil, err
	}

	paths.Tags = hostInfo

	return &paths, nil
}
