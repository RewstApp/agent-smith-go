package service

import "context"

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

// State is a registered service's state as the service manager reports it,
// reduced to what the agent acts on. Status.Raw keeps the manager's own
// words for the diagnostic to show.
type State int

const (
	// StateUnknown is a state the manager reported that the agent does not
	// classify (launchd printed no state line, say).
	StateUnknown State = iota
	StateRunning
	StateStopped
	// StatePending is a service on its way somewhere: systemd's activating
	// and deactivating, the SCM's StartPending and StopPending.
	StatePending
	StatePaused
)

func (s State) String() string {
	switch s {
	case StateRunning:
		return "running"
	case StateStopped:
		return "stopped"
	case StatePending:
		return "pending"
	case StatePaused:
		return "paused"
	default:
		return "unknown"
	}
}

// Status is the service manager's answer to what a registered service is
// doing (sc-132999).
type Status struct {
	State State
	// Raw is the manager's own state: systemd's ActiveState and SubState,
	// launchd's state line, the SCM's state name.
	Raw string
}

type Service interface {
	Start() error
	Stop() error
	Close() error
	Delete() error
	IsActive() bool
	// Status asks the service manager for the service's state. The query
	// runs under ctx as well as the platform's own bound, so a caller that
	// cannot wait the install paths' bound (the diagnostic) passes a
	// shorter deadline (sc-132999).
	Status(ctx context.Context) (Status, error)
	// Describe returns the service manager's own verbose account of the
	// service - what the diagnostic bundle's service.txt holds - under the
	// same bounds as Status.
	Describe(ctx context.Context) (string, error)
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

// ServiceLister is a ServiceManager that can enumerate the registered
// services by name. The Windows manager implements it for the diagnostic's
// SCM fallback scan, which finds an agent whose data directory this process
// cannot read or that was installed under another PROGRAMDATA (sc-132999).
type ServiceLister interface {
	ListServices() ([]string, error)
}
