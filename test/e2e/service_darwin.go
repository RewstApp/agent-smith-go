//go:build e2e && darwin

package e2e

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// launchdController drives the agent's launchd daemon through launchctl, the
// way the agent itself does (internal/service/service_darwin.go) and the way
// the workflow's helper scripts did (`launchctl stop <name>`).
type launchdController struct{ name string }

func newServiceController(name string) serviceController { return &launchdController{name: name} }

func requireElevation(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Fatal("the e2e scenarios install a system daemon and must run as root (sudo)")
	}
}

func (c *launchdController) target() string { return fmt.Sprintf("system/%s", c.name) }

func (c *launchdController) Status() (string, error) {
	out, err := runCommand("launchctl", "print", c.target())
	if err != nil {
		return "absent", nil
	}
	for ln := range strings.SplitSeq(out, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(ln), "=")
		if !found || strings.TrimSpace(key) != "state" {
			continue
		}
		switch strings.TrimSpace(value) {
		case "running":
			return "running", nil
		case "spawn scheduled", "spawning":
			return "start-pending", nil
		default:
			return "stopped", nil
		}
	}
	return "stopped", nil
}

func (c *launchdController) Exists() bool {
	_, err := runCommand("launchctl", "print", c.target())
	return err == nil
}

func (c *launchdController) Stop() error {
	_, err := runCommand("launchctl", "stop", c.name)
	return err
}

func (c *launchdController) Start() error {
	_, err := runCommand("launchctl", "start", c.name)
	return err
}

func (c *launchdController) Restart() error {
	_, err := runCommand("launchctl", "kickstart", "-k", c.target())
	return err
}

func (c *launchdController) Kill() error {
	_, err := runCommand("launchctl", "kill", "SIGKILL", c.target())
	return err
}

func (c *launchdController) BinaryPath() (string, error) { return "", errNotSupported }
func (c *launchdController) SetBinaryPath(string) error  { return errNotSupported }
