package agent

import (
	"context"

	"github.com/hashicorp/go-hclog"
)

// scriptsDirOverride replaces each platform's GetScriptsDirectory computed
// path when non-nil. It exists only for tests that need script execution to
// land in a real, writable scratch directory rather than the real
// installation path, which EnsureSecureDir hardens to a privileged,
// agent-owned directory that an unprivileged test run cannot create (on
// Linux/macOS) or which a documented customer AV/EDR exclusion is scoped to
// (on Windows — see GetScriptsDirectory in installation_windows.go). See
// SetScriptsDirectoryOverrideForTesting; nothing in a running agent ever
// calls it, so production always resolves through each platform's own path.
var scriptsDirOverride func(orgId string) string

// SetScriptsDirectoryOverrideForTesting redirects every platform's
// GetScriptsDirectory to fn for the remaining lifetime of the process;
// passing nil restores the production path. It is exported so tests outside
// this package (the interpreter package's own tests, and cmd/agent_smith
// tests that exercise the real startup sweep) can point script execution at a
// t.TempDir() instead of the real, privileged installation path.
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
