package main

import "github.com/RewstApp/agent-smith-go/internal/agent"

type fetchConfigurationResponse struct {
	Configuration agent.Device `json:"configuration"`
}
