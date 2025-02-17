package interpreter

import (
	"encoding/base64"
	"encoding/json"
)

type CommandDispatchMessage struct {
	PostId              string  `json:"post_id"`
	Commands            string  `json:"commands"`
	InterpreterOverride *string `json:"interpreter_override"`
}

type CommandDispatchResult struct {
	PostId       string
	Interpreter  string
	TempFilename string
	ExitCode     int
	Stderr       string
	Stdout       string
}

func (m *CommandDispatchMessage) GetCommandBytes() ([]byte, error) {
	content, err := base64.StdEncoding.DecodeString(m.Commands)
	if err != nil {
		return []byte{}, err
	}

	return content, nil
}

func (m *CommandDispatchMessage) Parse(data []byte) error {
	return json.Unmarshal(data, m)
}

func Execute(data []byte) (CommandDispatchResult, error) {
	var message CommandDispatchMessage
	err := message.Parse(data)
	if err != nil {
		return CommandDispatchResult{}, err
	}

	// Select the correct interpreter
	switch message.InterpreterOverride {
	// TODO: Support other interpreter
	default:
		return executeUsingPowershell(&message)
	}
}
