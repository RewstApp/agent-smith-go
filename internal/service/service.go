package service

type AgentParams struct {
	Name                string
	AgentExecutablePath string
	OrgId               string
	ConfigFilePath      string
	LogFilePath         string
	// ServiceUsername, when non-empty, registers the service to run as the
	// given account instead of the platform default (LocalSystem on Windows,
	// root on Linux/macOS).
	ServiceUsername string
	// ServicePassword is the password for ServiceUsername on Windows. It is
	// only consulted at service registration time and must never be persisted
	// to disk. Linux and macOS service managers ignore it.
	ServicePassword string
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

type ServiceManager interface {
	Create(params AgentParams) (Service, error)
	Open(name string) (Service, error)
}
