//go:build linux || darwin

package service

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/RewstApp/agent-smith-go/internal/utils"
	"github.com/hashicorp/go-hclog"
)

func Run(runner Runner) (int, error) {
	// Create a channel to listen for termination signals
	signalReceived := make(chan os.Signal, 1)
	signal.Notify(signalReceived, os.Interrupt, syscall.SIGTERM)

	// Make go routines for the channels
	stop := make(chan struct{})
	ctxStop, cancelStop := context.WithCancel(context.Background())
	defer cancelStop()

	utils.SafeGo(hclog.Default(), func() {
		select {
		case <-signalReceived:
			stop <- struct{}{}
		case <-ctxStop.Done():
		}
	}, "scope", "signal_monitor")

	running := make(chan struct{})
	ctxRunning, cancelRunning := context.WithCancel(context.Background())
	defer cancelRunning()

	utils.SafeGo(hclog.Default(), func() {
		select {
		case <-running:
		case <-ctxRunning.Done():
		}
	}, "scope", "running_monitor")

	// Execute the runner
	exitCode := runner.Execute(stop, running)

	return int(exitCode), nil
}
