package interpreter

import (
	"encoding/json"

	"github.com/hashicorp/go-hclog"
)

type errorResult struct {
	Error string `json:"error"`
}

type result struct {
	Error  string `json:"error"`
	Output string `json:"output"`
	// TimedOut is set when the command was killed because it exceeded the
	// configured per-command execution timeout, so the receiving workflow can
	// distinguish a timeout from a normal non-zero exit. Omitted for commands
	// that finished on their own.
	TimedOut bool `json:"timed_out,omitempty"`
	// Truncated is set when the command produced more output than the configured
	// per-command ceiling (see agent.Device.ResolvedMaxOutputBytes) and the
	// excess was discarded rather than buffered, so the receiving workflow can
	// tell a truncated result from a complete one instead of silently trusting a
	// partial one. OutputBytesProduced and OutputBytesKept carry the totals
	// across stdout and stderr, so the workflow can also see how much was
	// dropped. All three are omitted for output captured in full, leaving those
	// results byte-identical to previous releases.
	Truncated           bool  `json:"truncated,omitempty"`
	OutputBytesProduced int64 `json:"output_bytes_produced,omitempty"`
	OutputBytesKept     int64 `json:"output_bytes_kept,omitempty"`
}

// outputTruncation reports whether a command's captured output was cut short by
// the per-command output ceiling, along with how many bytes the command produced
// across stdout and stderr and how many of those were kept.
type outputTruncation struct {
	Truncated bool
	Produced  int64
	Kept      int64
}

// applyTo copies the truncation signal onto a result. Nothing is set when the
// output was captured in full, so an untruncated result marshals exactly as it
// did before the output ceiling existed.
func (t outputTruncation) applyTo(r *result) {
	if !t.Truncated {
		return
	}
	r.Truncated = true
	r.OutputBytesProduced = t.Produced
	r.OutputBytesKept = t.Kept
}

func errorResultBytes(logger hclog.Logger, err error) []byte {
	r := &errorResult{
		Error: err.Error(),
	}
	b, marshalErr := json.Marshal(r)
	if marshalErr != nil {
		logger.Error("Failed to marshal error result", "error", marshalErr)
		return []byte(`{"error":"failed to marshal error result"}`)
	}
	return b
}

func resultBytes(logger hclog.Logger, err string, out string, trunc outputTruncation) []byte {
	r := &result{
		Error:  err,
		Output: out,
	}
	trunc.applyTo(r)
	b, marshalErr := json.Marshal(r)
	if marshalErr != nil {
		logger.Error("Failed to marshal result", "error", marshalErr)
		return []byte(`{"error":"failed to marshal result","output":""}`)
	}
	return b
}

// timeoutResultBytes marshals the result of a command that was killed because it
// exceeded the configured per-command execution timeout. It carries whatever
// output the command produced before it was cancelled and sets TimedOut so the
// receiving workflow can tell a timeout apart from a normal non-zero exit. A
// command that was both verbose and hung carries the truncation signal alongside
// the timeout flag.
func timeoutResultBytes(
	logger hclog.Logger,
	errMsg string,
	out string,
	trunc outputTruncation,
) []byte {
	r := &result{
		Error:    errMsg,
		Output:   out,
		TimedOut: true,
	}
	trunc.applyTo(r)
	b, marshalErr := json.Marshal(r)
	if marshalErr != nil {
		logger.Error("Failed to marshal timeout result", "error", marshalErr)
		return []byte(`{"error":"command timed out","output":"","timed_out":true}`)
	}
	return b
}
