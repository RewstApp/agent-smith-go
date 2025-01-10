package interpreter

import (
	"encoding/base64"
	"encoding/json"
	"log"

	"github.com/RewstApp/agent-smith-go/internal/utils"
)

type CommandDispatchMessage struct {
	PostId              string  `json:"post_id"`
	Commands            string  `json:"commands"`
	InterpreterOverride *string `json:"interpreter_override"`
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

func Execute(data []byte, conf *utils.Config) error {
	var message CommandDispatchMessage
	err := message.Parse(data)
	if err != nil {
		return err
	}

	// Print contents of message
	log.Println("Received message:")
	log.Println("post_id", message.PostId)
	log.Println("commands", message.Commands)
	log.Println("interpreter_override", message.InterpreterOverride)

	// Select the correct interpreter
	switch message.InterpreterOverride {
	// TODO: Support other interpreter
	default:
		return executeUsingPowershell(&message, conf)
	}
}
