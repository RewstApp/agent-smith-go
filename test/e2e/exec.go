//go:build e2e && !windows

package e2e

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

var errNotSupported = errors.New("not supported on this platform")

// runCommand runs a service-manager command and returns its trimmed combined
// output; a non-zero exit is returned with the output in the error.
func runCommand(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), serviceCommandTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if ctx.Err() != nil {
		return text, fmt.Errorf(
			"%s %s timed out after %s",
			name,
			strings.Join(args, " "),
			serviceCommandTimeout,
		)
	}
	if err != nil {
		return text, fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, text)
	}
	return text, nil
}
