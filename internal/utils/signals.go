package utils

import (
	"os"
	"os/signal"
	"runtime"
	"syscall"
)

func MonitorSignal() <-chan os.Signal {
	// Create a channel to monitor incoming signals to closes
	signalChan := make(chan os.Signal, 1)

	if runtime.GOOS == "windows" {
		// Windows only supports os.Interrupt signal
		signal.Notify(signalChan, os.Interrupt)
	} else {
		signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	}

	return signalChan
}
