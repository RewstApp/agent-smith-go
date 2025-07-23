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

type ServiceExitCode int

const (
	GenericError ServiceExitCode = 1
	ConfigError  ServiceExitCode = 2
	LogFileError ServiceExitCode = 3
)

type Runner interface {
	Name() string
	Execute(stop <-chan struct{}, running chan<- struct{}) ServiceExitCode
}
