//go:build windows

package service

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/hashicorp/go-hclog"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const pollingInterval = 250 * time.Millisecond

// serviceStopTimeout bounds how long Stop waits for a service to report Stopped
// after the stop control has been accepted. Every caller of Stop is a blocking,
// user- or updater-initiated operation, so an unbounded wait turns a service
// that will not stop into an auto-update or uninstall that never returns.
//
// The value is deliberately generous: it exists to catch a wedged service, not
// to race a normal shutdown. A healthy agent finishes its own teardown in
// seconds, but it first drains whatever commands are in flight, and an operator
// can set command_timeout_seconds high enough that a single command legitimately
// runs for minutes. Five minutes sits far above any healthy shutdown while still
// being short enough that an unattended update fails with an actionable error
// the same day it runs.
const serviceStopTimeout = 5 * time.Minute

// stopPendingCheckpointInterval is how often Execute republishes StopPending
// with a fresh CheckPoint while the runner is still shutting down.
//
// The Service Control Manager watches a stopping service for progress: it wants
// either the next state or an incremented CheckPoint within the WaitHint the
// service published, and a service that goes quiet is reported as not
// responding (event 7009/7043) even when it is shutting down correctly. The
// agent's own shutdown can legitimately run for minutes — it drains in-flight
// command workers, each bounded by command_timeout_seconds, and waits on plugin
// shutdown — so the checkpoints have to keep coming for as long as that takes
// rather than being sent once at the start.
//
// Ten seconds is frequent enough to sit well inside stopPendingWaitHint while
// producing only a handful of status updates over a normal shutdown.
const stopPendingCheckpointInterval = 10 * time.Second

// stopPendingWaitHint is the WaitHint published with every StopPending status:
// how long the SCM should wait for the next checkpoint before treating the
// service as hung. It is three times stopPendingCheckpointInterval, so a
// checkpoint that is merely late — a scheduling hiccup, a busy endpoint — does
// not read as a wedge.
const stopPendingWaitHint = 3 * stopPendingCheckpointInterval

// stopTimeoutOverrideStr is overridable via -ldflags for integration testing.
// When set to a valid, positive Go duration it replaces serviceStopTimeout, so a
// wedged service can be observed being given up on in seconds rather than the
// production five minutes. It is empty in production builds.
// Example: -ldflags "-X github.com/RewstApp/agent-smith-go/internal/service.stopTimeoutOverrideStr=25s"
var stopTimeoutOverrideStr = ""

type windowsService struct {
	handle windowsServiceHandle

	// name is the registered service name. It is only used to name the service
	// in Stop's errors, so an aborted update or uninstall says which service
	// would not stop.
	name string

	// Test seams for the bounded stop wait. Zero values select the package
	// defaults, so production code never sets them.
	stopTimeout  time.Duration
	pollInterval time.Duration
	now          func() time.Time
	sleep        func(time.Duration)
}

// Replaces mgr.Service
type windowsServiceHandle interface {
	Close() error
	Start(args ...string) error
	Control(c svc.Cmd) (svc.Status, error)
	Query() (svc.Status, error)
	Delete() error
}

func (winSvc *windowsService) Close() error {
	return winSvc.handle.Close()
}

func (winSvc *windowsService) Start() error {
	return winSvc.handle.Start()
}

// serviceStateName renders a service state for logs and errors, so a failure
// reads as "StopPending" rather than as a bare number.
func serviceStateName(state svc.State) string {
	switch state {
	case svc.Stopped:
		return "Stopped"
	case svc.StartPending:
		return "StartPending"
	case svc.StopPending:
		return "StopPending"
	case svc.Running:
		return "Running"
	case svc.ContinuePending:
		return "ContinuePending"
	case svc.PausePending:
		return "PausePending"
	case svc.Paused:
		return "Paused"
	default:
		return fmt.Sprintf("Unknown(%d)", uint32(state))
	}
}

// stopInProgress reports whether state is one a service is expected to pass
// through on its way to Stopped once a stop control has been accepted, and is
// therefore worth polling until the deadline.
//
// StopPending is the normal case. Running and StartPending are tolerated because
// Control reports the status the service last published, which can still be the
// pre-stop state before the service thread picks the control up; treating those
// as failures would break healthy stops. Any other state means the service is
// not heading for Stopped at all, so waiting out the full deadline would only
// delay a failure that is already certain.
func stopInProgress(state svc.State) bool {
	switch state {
	case svc.StopPending, svc.Running, svc.StartPending:
		return true
	default:
		return false
	}
}

// resolveStopTimeout returns how long Stop waits for the service to reach
// Stopped: the per-instance value if one is set (unit tests only), then the
// ldflags-injected override (integration builds only), otherwise the documented
// serviceStopTimeout.
func (winSvc *windowsService) resolveStopTimeout() time.Duration {
	if winSvc.stopTimeout > 0 {
		return winSvc.stopTimeout
	}
	if stopTimeoutOverrideStr != "" {
		if timeout, err := time.ParseDuration(stopTimeoutOverrideStr); err == nil && timeout > 0 {
			return timeout
		}
	}
	return serviceStopTimeout
}

// Stop requests a stop and waits at most serviceStopTimeout for the service to
// reach Stopped. It never waits indefinitely: overrunning the deadline, or
// observing a state the service cannot reach Stopped from, returns an error
// naming the service and the last observed state.
func (winSvc *windowsService) Stop() error {
	status, err := winSvc.handle.Control(svc.Stop)
	if err != nil {
		return err
	}

	timeout := winSvc.resolveStopTimeout()
	interval := winSvc.pollInterval
	if interval <= 0 {
		interval = pollingInterval
	}
	now := winSvc.now
	if now == nil {
		now = time.Now
	}
	sleep := winSvc.sleep
	if sleep == nil {
		sleep = time.Sleep
	}

	// Wait for the service to stop by polling the status until the deadline
	deadline := now().Add(timeout)
	for {
		if status.State == svc.Stopped {
			return nil
		}

		if !stopInProgress(status.State) {
			return fmt.Errorf(
				"service %s will not stop: unexpected state %s",
				winSvc.name,
				serviceStateName(status.State),
			)
		}

		if !now().Before(deadline) {
			return fmt.Errorf(
				"service %s did not stop within %s: last observed state %s",
				winSvc.name,
				timeout,
				serviceStateName(status.State),
			)
		}

		sleep(interval)

		status, err = winSvc.handle.Query()
		if err != nil {
			return err
		}
	}
}

func (winSvc *windowsService) Delete() error {
	return winSvc.handle.Delete()
}

func (winSvc *windowsService) IsActive() bool {
	status, err := winSvc.handle.Query()
	if err != nil {
		return false
	}

	return status.State == svc.Running
}

// Substitute for mgr.Mgr
type windowsServiceManager interface {
	Disconnect() error
	CreateService(name string, exepath string, c mgr.Config, args ...string) (*mgr.Service, error)
	OpenService(name string) (*mgr.Service, error)
}

type windowsServiceManagerFactory interface {
	Connect() (windowsServiceManager, error)
}

type defaultWindowsServiceManagerFactory struct{}

func (f *defaultWindowsServiceManagerFactory) Connect() (windowsServiceManager, error) {
	return mgr.Connect()
}

type defaultServiceManager struct {
	factory windowsServiceManagerFactory
}

func (s *defaultServiceManager) Create(params AgentParams) (Service, error) {
	svcMgr, err := s.factory.Connect()
	if err != nil {
		return nil, err
	}
	defer func() { _ = svcMgr.Disconnect() }() // Cleanup - error can be ignored

	config := mgr.Config{
		StartType:        mgr.StartAutomatic,
		Description:      fmt.Sprintf("Rewst Remote Agent for Org %s", params.OrgId),
		DelayedAutoStart: true,
	}
	if params.ServiceUsername != "" {
		config.ServiceStartName = params.ServiceUsername
		config.Password = params.ServicePassword
	}

	svc, err := svcMgr.CreateService(
		params.Name,
		params.AgentExecutablePath,
		config,
		"--org-id",
		params.OrgId,
		"--config-file",
		params.ConfigFilePath,
		"--log-file",
		params.LogFilePath,
	)
	if err != nil {
		return nil, err
	}

	return &windowsService{
		handle: svc,
		name:   params.Name,
	}, nil
}

func (s *defaultServiceManager) Open(name string) (Service, error) {
	svcMgr, err := s.factory.Connect()
	if err != nil {
		return nil, err
	}
	defer func() { _ = svcMgr.Disconnect() }() // Cleanup - error can be ignored

	svc, err := svcMgr.OpenService(name)
	if err != nil {
		return nil, err
	}

	return &windowsService{
		handle: svc,
		name:   name,
	}, nil
}

func NewServiceManager() ServiceManager {
	return &defaultServiceManager{
		factory: &defaultWindowsServiceManagerFactory{},
	}
}

type windowsRunner struct {
	runner   Runner
	exitCode int

	// checkpointInterval overrides stopPendingCheckpointInterval. It is a test
	// seam only; the zero value selects the package default, so production code
	// never sets it.
	checkpointInterval time.Duration
}

// statusReporter serializes the status updates Execute publishes to the Service
// Control Manager and drops any that would walk the service lifecycle backwards.
//
// Execute reports from more than one goroutine: the running monitor publishes
// Running, and the request monitor publishes StopPending and then keeps
// checkpointing it while the runner drains. Those goroutines race each other and
// the shutdown, so the reporter enforces the order the SCM expects:
//
//   - Stopped is terminal. The dispatcher stops reading the response channel
//     once it arrives, so a later send would both contradict the status and
//     block its goroutine forever on a channel nobody is reading.
//   - Running is dropped once a stop is in progress. A stop control can arrive
//     before the agent finishes starting up, and telling the SCM the service is
//     Running after it acknowledged StopPending would report it as healthy again
//     and re-advertise the controls it accepts.
type statusReporter struct {
	mu       sync.Mutex
	response chan<- svc.Status
	stopping bool
	stopped  bool
}

// report publishes status unless it would walk the service lifecycle backwards,
// in which case it is dropped.
func (reporter *statusReporter) report(status svc.Status) {
	reporter.mu.Lock()
	defer reporter.mu.Unlock()

	if reporter.stopped {
		return
	}

	switch status.State {
	case svc.Running:
		if reporter.stopping {
			return
		}
	case svc.StopPending:
		reporter.stopping = true
	case svc.Stopped:
		reporter.stopped = true
	}

	reporter.response <- status
}

// stopPendingStatus builds a StopPending status carrying the given progress
// checkpoint. WaitHint is in milliseconds.
func stopPendingStatus(checkpoint uint32) svc.Status {
	return svc.Status{
		State:      svc.StopPending,
		CheckPoint: checkpoint,
		WaitHint:   uint32(stopPendingWaitHint / time.Millisecond),
	}
}

// reportStopProgress republishes StopPending with an incrementing CheckPoint
// until ctx is canceled, which happens when the runner has finished shutting
// down. The first StopPending is published by the caller before the runner is
// asked to stop, so the SCM has the acknowledgment in hand before any draining
// starts.
func (host *windowsRunner) reportStopProgress(ctx context.Context, reporter *statusReporter) {
	interval := host.checkpointInterval
	if interval <= 0 {
		interval = stopPendingCheckpointInterval
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// The immediate StopPending the caller already sent is checkpoint 1.
	checkpoint := uint32(1)
	for {
		select {
		case <-ticker.C:
			checkpoint++
			reporter.report(stopPendingStatus(checkpoint))
		case <-ctx.Done():
			return
		}
	}
}

func (host *windowsRunner) Execute(
	args []string,
	request <-chan svc.ChangeRequest,
	response chan<- svc.Status,
) (bool, uint32) {
	reporter := &statusReporter{response: response}
	reporter.report(svc.Status{State: svc.StartPending})

	// Make the channels
	stop := make(chan struct{}, 1)
	running := make(chan struct{})

	// Make go routines for the channels
	ctxStop, cancelStop := context.WithCancel(context.Background())
	defer cancelStop()
	utils.SafeGo(hclog.Default(), func() {
		for {
			select {
			case change := <-request:
				switch change.Cmd {
				case svc.Stop, svc.Shutdown:
					// Acknowledge the stop to the SCM before the runner starts
					// draining, then keep checkpointing for as long as the
					// shutdown takes. Without this the service jumped straight
					// from Running to Stopped, and a shutdown that spent
					// minutes draining a long-running command looked like a
					// hang to Windows and to anything watching service state.
					reporter.report(stopPendingStatus(1))
					stop <- struct{}{}
					host.reportStopProgress(ctxStop, reporter)
					return
				}
			case <-ctxStop.Done():
				// Stop this routine
				return
			}
		}
	}, "scope", "request_monitor")

	ctxRunning, cancelRunning := context.WithCancel(context.Background())
	defer cancelRunning()
	utils.SafeGo(hclog.Default(), func() {
		select {
		case <-running:
			reporter.report(svc.Status{
				State:   svc.Running,
				Accepts: svc.AcceptStop | svc.AcceptShutdown,
			})
		case <-ctxRunning.Done():
			// Stop this routine
			return
		}
	}, "scope", "running_monitor")

	// Execute the runner
	host.exitCode = int(host.runner.Execute(stop, running))

	// Shutdown is over: end the checkpoints before publishing Stopped so the
	// SCM does not see progress reported after the service has stopped.
	cancelStop()
	reporter.report(svc.Status{State: svc.Stopped})

	// Return the proper response
	if host.exitCode < 0 || host.exitCode > math.MaxUint32 {
		return false, uint32(GenericError)
	}
	return host.exitCode == 0, uint32(host.exitCode)
}

type windowsServiceFactory interface {
	IsWindowsService() (bool, error)
	Run(name string, handler svc.Handler) error
}

type defaultWindowsServiceFactory struct{}

func (f *defaultWindowsServiceFactory) IsWindowsService() (bool, error) {
	return svc.IsWindowsService()
}

func (f *defaultWindowsServiceFactory) Run(name string, handler svc.Handler) error {
	return svc.Run(name, handler)
}

func Run(runner Runner) (int, error) {
	return runWithFactory(runner, &defaultWindowsServiceFactory{})
}

func runWithFactory(runner Runner, factory windowsServiceFactory) (int, error) {
	// Check if this is running as a service
	isWinSvc, err := factory.IsWindowsService()
	if err != nil {
		return int(GenericError), err
	}

	if !isWinSvc {
		return int(GenericError), fmt.Errorf("executable should be run as a service")
	}

	// Start the windows service
	host := &windowsRunner{
		runner: runner,
	}
	err = factory.Run(runner.Name(), host)
	if err != nil {
		return host.exitCode, err
	}

	return host.exitCode, nil
}
