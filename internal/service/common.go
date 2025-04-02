package service

type AgentParams struct {
	Name                string
	AgentExecutablePath string
	OrgId               string
	ConfigFilePath      string
	LogFilePath         string
}

type Service interface {
	Start() error
	Stop() error
	Close() error
	Delete() error
	IsActive() bool
}
