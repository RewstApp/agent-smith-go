package interpreter

import "encoding/json"

type errorResult struct {
	Error string `json:"error"`
}

type result struct {
	Error  string `json:"error"`
	Output string `json:"output"`
}

func errorResultBytes(err error) []byte {
	result := &errorResult{
		Error: err.Error(),
	}
	bytes, _ := json.MarshalIndent(result, "", "  ")
	return bytes
}

func resultBytes(err string, out string) []byte {
	result := &result{
		Error:  err,
		Output: out,
	}
	bytes, _ := json.MarshalIndent(result, "", "  ")
	return bytes
}
