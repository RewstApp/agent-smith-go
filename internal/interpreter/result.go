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

func resultBytes(logger hclog.Logger, err string, out string) []byte {
	r := &result{
		Error:  err,
		Output: out,
	}
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
// receiving workflow can tell a timeout apart from a normal non-zero exit.
func timeoutResultBytes(logger hclog.Logger, errMsg string, out string) []byte {
	r := &result{
		Error:    errMsg,
		Output:   out,
		TimedOut: true,
	}
	b, marshalErr := json.Marshal(r)
	if marshalErr != nil {
		logger.Error("Failed to marshal timeout result", "error", marshalErr)
		return []byte(`{"error":"command timed out","output":"","timed_out":true}`)
	}
	return b
}
