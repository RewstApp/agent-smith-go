//go:build integration && !windows

// The wedgedservice fixture reproduces a Windows service stuck in
// SERVICE_STOP_PENDING (sc-106108), a state with no equivalent on Linux or
// macOS, where the service implementations do not poll for a stop at all. This
// file exists only so the package still builds on those platforms - without it
// `go build -tags integration ./...` and the linter fail on a package whose only
// file is excluded by build constraints. See main.go for the fixture itself.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "wedgedservice is a Windows-only fixture")
	os.Exit(1)
}
