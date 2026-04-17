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
}

func errorResultBytes(logger hclog.Logger, err error) []byte {
	r := &errorResult{
		Error: err.Error(),
	}
	b, marshalErr := json.MarshalIndent(r, "", "  ")
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
	b, marshalErr := json.MarshalIndent(r, "", "  ")
	if marshalErr != nil {
		logger.Error("Failed to marshal result", "error", marshalErr)
		return []byte(`{"error":"failed to marshal result","output":""}`)
	}
	return b
}
