//go:build integration && windows

// Command wedgedservice is a deliberately unstoppable Windows service used by
// the integration test suite to reproduce the failure mode sc-106108 fixed: a
// service that accepts SERVICE_CONTROL_STOP, returns from its control handler,
// reports SERVICE_STOP_PENDING — and then never reaches Stopped.
//
// That is the state a wedged agent leaves behind (a process parked in an
// unbounded wait, or Windows holding the service in StopPending behind a stuck
// operation), and it used to hang every caller of windowsService.Stop()
// indefinitely: an unattended auto-update that never finished and an uninstall
// that never returned. It cannot be produced by killing the agent — a dead
// process makes the SCM report Stopped almost immediately, which is the happy
// path — and suspending the process instead is not deterministic, because the
// SCM then fails the control request outright rather than exercising the wait.
// So the fixture stands in for the wedge itself: the ticket's own framing is
// that any wedge will do, because what is under test is the bound on the wait
// and not how the agent got stuck.
//
// The harness registers it under the agent's own service name by rewriting that
// service's binPath (see .github/actions/wedged-service), so the agent binary
// and config file on disk are untouched and can be hashed before and after the
// aborted update.
//
// The wedge is escapable so the runner can be recovered without a force-kill:
// once -release exists the fixture reports Stopped and exits, and the harness
// restores the original binPath. Every transition is appended to -log, since a
// service's stdout goes nowhere and that log is the only account of what the
// fixture did.
//
// It sits behind the `integration` build tag so it stays out of
// `go test ./...`: as an untested CI fixture its statements would otherwise
// count against the repository coverage threshold. It is still linted, because
// the tag is listed in .golangci.yml. Build it with
// `go build -tags integration ./test/wedgedservice`.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows/svc"
)

// stopWaitHint is reported alongside StopPending. It is long enough to cover the
// agent's production stop deadline, so the SCM keeps reporting StopPending for
// the whole scenario instead of deciding the service has stopped responding.
const stopWaitHint = 10 * time.Minute

// releasePollInterval is how often the wedged handler looks for the release
// file. The wedge only has to outlive the caller's deadline, so polling once a
// second is ample and keeps teardown prompt.
const releasePollInterval = time.Second

type wedgedService struct {
	releasePath string
	logf        func(format string, args ...any)
}

func (w *wedgedService) Execute(
	args []string,
	requests <-chan svc.ChangeRequest,
	responses chan<- svc.Status,
) (bool, uint32) {
	responses <- svc.Status{State: svc.StartPending}
	responses <- svc.Status{
		State:   svc.Running,
		Accepts: svc.AcceptStop | svc.AcceptShutdown,
	}
	w.logf("running; awaiting a stop control")

	for {
		request := <-requests
		switch request.Cmd {
		case svc.Interrogate:
			responses <- request.CurrentStatus
		case svc.Stop, svc.Shutdown:
			// Accept the control and report StopPending, exactly as a healthy
			// service beginning its teardown would - then never finish it. The
			// handler returning is what makes ControlService succeed, so the
			// caller reaches its polling wait rather than failing outright.
			responses <- svc.Status{
				State:    svc.StopPending,
				WaitHint: uint32(stopWaitHint / time.Millisecond),
			}
			w.logf("stop control accepted; wedged in StopPending until %s exists", w.releasePath)

			for {
				if _, err := os.Stat(w.releasePath); err == nil {
					w.logf("release file observed; reporting Stopped")
					responses <- svc.Status{State: svc.Stopped}
					return false, 0
				}
				time.Sleep(releasePollInterval)
			}
		default:
			w.logf("ignoring unexpected control %d", request.Cmd)
		}
	}
}

// newLogger appends timestamped lines to path. The file is reopened per line so
// the harness can read a complete log while the fixture is still wedged, and a
// logging failure is never fatal: the fixture's job is to stay unstoppable.
func newLogger(path string) func(string, ...any) {
	return func(format string, args ...any) {
		if path == "" {
			return
		}
		file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err != nil {
			return
		}
		defer func() { _ = file.Close() }()
		_, _ = fmt.Fprintf(
			file,
			"%s %s\n",
			time.Now().Format(time.RFC3339),
			fmt.Sprintf(format, args...),
		)
	}
}

func main() {
	name := flag.String("name", "", "service name to register in the dispatch table")
	logPath := flag.String("log", "", "file to append fixture log lines to")
	releasePath := flag.String(
		"release",
		"",
		"file whose existence releases the wedge and lets the service stop",
	)
	flag.Parse()

	logf := newLogger(*logPath)

	if *releasePath == "" {
		logf(
			"refusing to start: -release is required, an unreleasable wedge would strand the service",
		)
		fmt.Fprintln(os.Stderr, "-release is required")
		os.Exit(2)
	}

	// A release file left behind by an earlier run would release the wedge
	// immediately, which reads as "the service stopped fine" and would silently
	// pass the scenario.
	if _, err := os.Stat(*releasePath); err == nil {
		logf("refusing to start: release file %s already exists", *releasePath)
		fmt.Fprintf(os.Stderr, "release file %s already exists\n", *releasePath)
		os.Exit(2)
	}

	logf("starting as service %q", *name)
	if err := svc.Run(*name, &wedgedService{releasePath: *releasePath, logf: logf}); err != nil {
		logf("svc.Run failed: %v", err)
		fmt.Fprintf(os.Stderr, "svc.Run failed: %v\n", err)
		os.Exit(1)
	}
	logf("service exited")
}
