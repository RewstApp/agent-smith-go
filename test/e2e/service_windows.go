//go:build e2e && windows

package e2e

import (
	"fmt"
	"testing"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// scmController drives the agent's Windows service through the Service Control
// Manager API, and can rewrite its binary path for the wedged-service scenario.
type scmController struct{ name string }

func newServiceController(name string) serviceController { return &scmController{name: name} }

func requireElevation(t *testing.T) {
	t.Helper()
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Fatal("the e2e scenarios install a Windows service and must run from an elevated shell")
	}
}

// withService opens the service and runs fn against it.
func (c *scmController) withService(fn func(s *mgr.Service) error) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connecting to the service control manager: %w", err)
	}
	defer func() { _ = m.Disconnect() }()
	s, err := m.OpenService(c.name)
	if err != nil {
		return fmt.Errorf("opening service %s: %w", c.name, err)
	}
	defer func() { _ = s.Close() }()
	return fn(s)
}

func (c *scmController) Status() (string, error) {
	if !c.Exists() {
		return "absent", nil
	}
	var state svc.State
	err := c.withService(func(s *mgr.Service) error {
		st, err := s.Query()
		if err != nil {
			return err
		}
		state = st.State
		return nil
	})
	if err != nil {
		return "", err
	}
	switch state {
	case svc.Running:
		return "running", nil
	case svc.StartPending, svc.ContinuePending:
		return "start-pending", nil
	case svc.StopPending:
		return "stop-pending", nil
	case svc.Stopped:
		return "stopped", nil
	}
	return "", fmt.Errorf("unexpected service state %d for %s", state, c.name)
}

func (c *scmController) Exists() bool {
	return c.withService(func(*mgr.Service) error { return nil }) == nil
}

func (c *scmController) Stop() error {
	return c.withService(func(s *mgr.Service) error {
		_, err := s.Control(svc.Stop)
		return err
	})
}

func (c *scmController) Start() error {
	return c.withService(func(s *mgr.Service) error { return s.Start() })
}

// Restart stops the service, waits for Stopped and starts it again: the SCM has
// no restart control.
func (c *scmController) Restart() error {
	if err := c.Stop(); err != nil {
		return err
	}
	if !waitFor(serviceCommandTimeout, func() bool { s, _ := c.Status(); return s == "stopped" }) {
		return fmt.Errorf("%s did not reach Stopped within %s", c.name, serviceCommandTimeout)
	}
	return c.Start()
}

func (c *scmController) Kill() error {
	return c.withService(func(s *mgr.Service) error {
		st, err := s.Query()
		if err != nil {
			return err
		}
		if st.ProcessId == 0 {
			return nil
		}
		p, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, st.ProcessId)
		if err != nil {
			return fmt.Errorf("opening service process %d: %w", st.ProcessId, err)
		}
		defer func() { _ = windows.CloseHandle(p) }()
		return windows.TerminateProcess(p, 1)
	})
}

func (c *scmController) BinaryPath() (string, error) {
	var path string
	err := c.withService(func(s *mgr.Service) error {
		cfg, err := s.Config()
		if err != nil {
			return err
		}
		path = cfg.BinaryPathName
		return nil
	})
	return path, err
}

func (c *scmController) SetBinaryPath(path string) error {
	return c.withService(func(s *mgr.Service) error {
		cfg, err := s.Config()
		if err != nil {
			return err
		}
		cfg.BinaryPathName = path
		return s.UpdateConfig(cfg)
	})
}
