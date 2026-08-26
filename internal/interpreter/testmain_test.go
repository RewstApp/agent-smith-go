package interpreter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/RewstApp/agent-smith-go/internal/agent"
)

// TestMain points agent.GetScriptsDirectory at a scratch directory for this
// package's whole test run. Every test in this package exercises real script
// execution — a real file a real shell opens by path — which needs a real,
// writable directory; the production path is deliberately not one (see
// EnsureSecureDir).
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "agent-smith-scripts-test-*")
	if err != nil {
		panic(err)
	}

	agent.SetScriptsDirectoryOverrideForTesting(func(orgId string) string {
		return filepath.Join(dir, orgId)
	})

	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
