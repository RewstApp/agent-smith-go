//go:build e2e && linux

package e2e

import (
	"fmt"
	"os"
	"testing"
)

// systemdController drives the agent's systemd unit the way the agent itself
// does (internal/service/service_linux.go), through systemctl.
type systemdController struct{ name string }

func newServiceController(name string) serviceController { return &systemdController{name: name} }

func requireElevation(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Fatal("the e2e scenarios install a system service and must run as root (sudo)")
	}
}

func (c *systemdController) Status() (string, error) {
	if !c.Exists() {
		return "absent", nil
	}
	out, _ := runCommand("systemctl", "show", "-p", "ActiveState", "--value", c.name)
	switch out {
	case "active":
		return "running", nil
	case "activating", "reloading":
		return "start-pending", nil
	case "deactivating":
		return "stop-pending", nil
	case "inactive", "failed":
		return "stopped", nil
	}
	return "", fmt.Errorf("unexpected ActiveState %q for %s", out, c.name)
}

func (c *systemdController) Exists() bool {
	out, err := runCommand("systemctl", "show", "-p", "LoadState", "--value", c.name)
	return err == nil && out == "loaded"
}

func (c *systemdController) Stop() error {
	_, err := runCommand("systemctl", "stop", c.name)
	return err
}

func (c *systemdController) Start() error {
	_, err := runCommand("systemctl", "start", c.name)
	return err
}

func (c *systemdController) Restart() error {
	_, err := runCommand("systemctl", "restart", c.name)
	return err
}

func (c *systemdController) Kill() error {
	_, err := runCommand("systemctl", "kill", "-s", "KILL", c.name)
	return err
}

func (c *systemdController) BinaryPath() (string, error) { return "", errNotSupported }
func (c *systemdController) SetBinaryPath(string) error  { return errNotSupported }
