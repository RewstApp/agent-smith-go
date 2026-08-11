package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/service"
	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/hashicorp/go-hclog"
)

const (
	// agentProcessExitTimeout bounds how long install, update and uninstall wait
	// for the old agent process to actually exit before replacing or deleting its
	// files.
	//
	// The wait returns as soon as the process is gone, so a healthy endpoint pays
	// only the time its shutdown genuinely takes — normally a fraction of a
	// second. The deadline exists to catch a process that never exits, and is
	// sized for a shutdown that is slow but legitimate: the agent drains the
	// commands in flight, tears down the MQTT connection, and kills its plugin
	// subprocesses one at a time, each waiting out go-plugin's graceful-exit
	// grace period. Two minutes sits far above any healthy shutdown while still
	// failing an unattended update the same run it started, with an actionable
	// error.
	agentProcessExitTimeout = 2 * time.Minute

	// agentProcessExitPollInterval is how often the wait re-observes the exit
	// signals. It only controls how quickly a completed exit is noticed; the wait
	// never treats an elapsed interval as evidence of anything.
	agentProcessExitPollInterval = 250 * time.Millisecond

	// serviceDeregisterTimeout bounds how long the update path waits for a
	// deleted service registration to disappear before re-registering the service
	// under a new account. On Windows the Service Control Manager only reaps the
	// registration once every handle to it is closed, so the name can stay
	// visible briefly after Delete returns.
	serviceDeregisterTimeout = 30 * time.Second
)

// exitTimeoutOverrideStr is overridable via -ldflags for integration testing and
// QA. When set to a valid, positive Go duration it replaces
// agentProcessExitTimeout, so a process that never exits can be observed being
// given up on in seconds rather than the production two minutes. It is empty in
// production builds.
// Example: -ldflags "-X main.exitTimeoutOverrideStr=25s"
var exitTimeoutOverrideStr = ""

// resolveExitTimeout returns the deadline the exit wait uses: the
// ldflags-injected override when a build sets a valid one, otherwise the
// documented agentProcessExitTimeout.
func resolveExitTimeout() time.Duration {
	if exitTimeoutOverrideStr != "" {
		if timeout, err := time.ParseDuration(exitTimeoutOverrideStr); err == nil && timeout > 0 {
			return timeout
		}
	}
	return agentProcessExitTimeout
}

// exitWaitOptions are the test seams for the bounded waits below. Zero values
// select the documented package defaults, so production code never sets them.
type exitWaitOptions struct {
	timeout           time.Duration
	deregisterTimeout time.Duration
	pollInterval      time.Duration
	now               func() time.Time
	sleep             func(time.Duration)
	// processRunning reports whether a process is still executing the agent
	// binary. It defaults to a scan of the running processes.
	processRunning func(path string) (bool, error)
}

// resolved returns a copy with every unset field replaced by its default.
func (opts exitWaitOptions) resolved() exitWaitOptions {
	if opts.timeout <= 0 {
		opts.timeout = resolveExitTimeout()
	}
	if opts.deregisterTimeout <= 0 {
		opts.deregisterTimeout = serviceDeregisterTimeout
	}
	if opts.pollInterval <= 0 {
		opts.pollInterval = agentProcessExitPollInterval
	}
	if opts.now == nil {
		opts.now = time.Now
	}
	if opts.sleep == nil {
		opts.sleep = time.Sleep
	}
	if opts.processRunning == nil {
		opts.processRunning = utils.ProcessRunningFromExecutable
	}
	return opts
}

// waitForAgentProcessExit blocks until the old agent process is really gone. It
// waits on three direct observations of the process, all of which must clear:
// the service manager no longer reports the service active, no process is still
// executing the agent binary, and the executable is no longer held open as a
// running image. The wait never concludes "exited" merely because a poll
// interval or a fixed sleep elapsed, which is what made a five second sleep race
// a slow shutdown and fail the update with a sharing violation.
//
// The three overlap deliberately: the file signal is what actually blocks the
// write on Windows, but macOS lets a running image be opened for writing, and a
// service manager can report a service stopped while its process is still
// winding down.
//
// It returns as soon as every signal holds, so an endpoint that shuts down
// promptly costs a single round of probes rather than an unconditional wait. If
// any signal is still outstanding at the deadline it returns an error naming
// what it was still waiting for and for how long, and the caller aborts without
// touching the installation.
func waitForAgentProcessExit(
	logger hclog.Logger,
	svc service.Service,
	fsys utils.FileSystem,
	executablePath string,
	opts exitWaitOptions,
) error {
	opts = opts.resolved()

	start := opts.now()
	deadline := start.Add(opts.timeout)
	fileProbeErrorLogged := false
	processProbeErrorLogged := false

	for {
		var pending []string

		if svc != nil && svc.IsActive() {
			pending = append(pending, "the service manager still reports the service active")
		}

		running, err := opts.processRunning(executablePath)
		switch {
		case err != nil:
			// Same reasoning as the file probe below: a scan that could not run is
			// not evidence either way, so it must not pin the wait open.
			if !processProbeErrorLogged {
				logger.Warn(
					"Cannot scan for the agent process; relying on the remaining signals",
					"path", executablePath,
					"error", err,
				)
				processProbeErrorLogged = true
			}
		case running:
			pending = append(
				pending,
				fmt.Sprintf("a process is still running %s", executablePath),
			)
		}

		inUse, err := fsys.ExecutableInUse(executablePath)
		switch {
		case err != nil:
			// The probe itself could not run (a restrictive ACL, an unreadable
			// parent directory). That is not evidence the process is alive, and the
			// executable replacement is atomic either way, so fall back to the
			// remaining signals rather than blocking every update on a probe that
			// can never succeed on this endpoint.
			if !fileProbeErrorLogged {
				logger.Warn(
					"Cannot probe whether the agent executable is still open; relying on the remaining signals",
					"path",
					executablePath,
					"error",
					err,
				)
				fileProbeErrorLogged = true
			}
		case inUse:
			pending = append(
				pending,
				fmt.Sprintf("the agent executable %s is still held open", executablePath),
			)
		}

		if len(pending) == 0 {
			logger.Info(
				"Agent process exited",
				"waited",
				opts.now().Sub(start).Round(time.Millisecond).String(),
			)
			return nil
		}

		if !opts.now().Before(deadline) {
			return fmt.Errorf(
				"agent process did not exit within %s: %s",
				opts.timeout,
				strings.Join(pending, "; "),
			)
		}

		opts.sleep(opts.pollInterval)
	}
}

// waitForServiceDeregistration blocks until the service manager no longer knows
// about name, which is the real signal that a deleted registration has been
// reaped and the name is free to register again. Each probe closes its handle
// immediately, since on Windows an open handle is itself what keeps a deleted
// registration alive.
//
// Overrunning the deadline is reported to the caller rather than being treated
// as success; the caller decides whether re-registering anyway is better than
// leaving the endpoint with no service at all.
func waitForServiceDeregistration(
	mgr service.ServiceManager,
	name string,
	opts exitWaitOptions,
) error {
	opts = opts.resolved()

	deadline := opts.now().Add(opts.deregisterTimeout)
	for {
		svc, err := mgr.Open(name)
		if err != nil {
			// The manager no longer knows the service: the registration is gone.
			return nil
		}
		if svc != nil {
			_ = svc.Close()
		}

		if !opts.now().Before(deadline) {
			return fmt.Errorf(
				"service %s was still registered %s after it was deleted",
				name,
				opts.deregisterTimeout,
			)
		}

		opts.sleep(opts.pollInterval)
	}
}

// writeFileAtomic writes data to path without ever exposing a partially written
// file: the bytes go to a temporary file alongside the destination and are then
// renamed into place, which is a single atomic operation on every platform the
// agent runs on. A failed or interrupted write therefore leaves the previous
// file byte-identical instead of truncated — the same pattern the postback spool
// uses for its entries.
//
// The temporary file lives in the destination directory so the rename never
// crosses a filesystem boundary.
func writeFileAtomic(fsys utils.FileSystem, path string, data []byte, perm os.FileMode) error {
	tempPath := path + ".new"

	if err := fsys.WriteFile(tempPath, data, perm); err != nil {
		return err
	}

	if err := fsys.Rename(tempPath, path); err != nil {
		// Leave the destination as it was and take the half-written temp file with
		// us, so a retry does not inherit it.
		if removeErr := fsys.Remove(tempPath); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("%w (temporary file %s left behind: %v)", err, tempPath, removeErr)
		}
		return err
	}

	return nil
}
