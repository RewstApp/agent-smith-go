//go:build linux || darwin

package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// commandWaitDelay backs the deadline's kill up: a descendant of the service
// manager's command that escaped the kill but still holds the output pipe is
// abandoned after it, so CombinedOutput cannot block past the bound. The
// diagnostic's own shell-outs set it before sc-132999; now every launchctl
// and systemctl call does.
const commandWaitDelay = time.Second

// runOutput runs the service manager's binary with args under ctx and the
// platform's own timeout, whichever ends first, and returns the combined
// output. A non-zero exit returns the output with exec's error so the
// caller can show both (Describe) or classify (Run); a deadline kills the
// command and returns an error naming it, the bound and whatever it
// printed, wrapping ctx's error so callers can tell a timeout from a refusal.
func runOutput(
	ctx context.Context,
	tool, binary string,
	timeout time.Duration,
	args ...string,
) ([]byte, error) {
	bound := timeout
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < bound {
			bound = remaining
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// #nosec G204 - a fixed service-manager binary; the arguments come from
	// the agent's own service naming.
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.WaitDelay = commandWaitDelay
	out, err := cmd.CombinedOutput()
	if err == nil {
		return out, nil
	}
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		msg := fmt.Sprintf("%s %s timed out after %s", tool, strings.Join(args, " "), bound)
		if printed := strings.TrimSpace(string(out)); printed != "" {
			msg += ": " + printed
		}
		return out, fmt.Errorf("%s: %w", msg, ctx.Err())
	case ctx.Err() != nil:
		return out, fmt.Errorf("%s %s canceled: %w", tool, strings.Join(args, " "), ctx.Err())
	}
	return out, err
}

// runFailure is the error Run reports for a command that did not succeed:
// the timeout as runOutput described it, otherwise the command's own output,
// which is what the install, update and uninstall paths have always shown.
func runFailure(out []byte, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return err
	}
	return fmt.Errorf("%s", out)
}

// describeWith runs each query and records its command line and output the
// way the diagnostic bundle's service.txt has shown them since sc-129690: a
// query that fails still contributes what it printed, with the exit beside
// it, because a stopped unit's `systemctl status` exits non-zero and is the
// output the support case wants.
func describeWith(
	ctx context.Context,
	tool string,
	run func(ctx context.Context, args ...string) ([]byte, error),
	queries [][]string,
) (string, error) {
	var buf bytes.Buffer
	for _, args := range queries {
		out, err := run(ctx, args...)
		_, _ = fmt.Fprintf(&buf, "$ %s %s\n%s", tool, strings.Join(args, " "), out)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				return buf.String(), err
			}
			_, _ = fmt.Fprintf(&buf, "(exit: %v)", err)
		}
		_, _ = buf.WriteString("\n")
	}
	return buf.String(), nil
}
