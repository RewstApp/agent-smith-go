package main

import (
	"context"
	"errors"
	"os"

	"github.com/RewstApp/agent-smith-go/internal/service"
)

type mockSystemInfoProvider struct {
	hostname            string
	hostnameErr         error
	hostPlatform        string
	hostPlatformErr     error
	cpuModelName        string
	cpuModelNameErr     error
	totalMemoryBytes    uint64
	totalMemoryBytesErr error
	macAddress          *string
	macAddressErr       error
}

func (mock *mockSystemInfoProvider) Hostname() (string, error) {
	return mock.hostname, mock.hostnameErr
}

func (mock *mockSystemInfoProvider) HostPlatform() (string, error) {
	return mock.hostPlatform, mock.hostPlatformErr
}

func (mock *mockSystemInfoProvider) CPUModelName() (string, error) {
	return mock.cpuModelName, mock.cpuModelNameErr
}

func (mock *mockSystemInfoProvider) TotalMemoryBytes() (uint64, error) {
	return mock.totalMemoryBytes, mock.totalMemoryBytesErr
}

func (mock *mockSystemInfoProvider) MACAddress() (*string, error) {
	return mock.macAddress, mock.macAddressErr
}

type mockDomainInfoProvider struct {
	adDomain                *string
	adDomainErr             error
	isAdDomainController    bool
	isAdDomainControllerErr error
	isEntraConnectServer    bool
	isEntraConnectServerErr error
	entraDomain             *string
	entraDomainErr          error
}

func (mock *mockDomainInfoProvider) ADDomain(context.Context) (*string, error) {
	return mock.adDomain, mock.adDomainErr
}

func (mock *mockDomainInfoProvider) IsADDomainController(context.Context) (bool, error) {
	return mock.isAdDomainController, mock.isAdDomainControllerErr
}

func (mock *mockDomainInfoProvider) IsEntraConnectServer() (bool, error) {
	return mock.isEntraConnectServer, mock.isEntraConnectServerErr
}

func (mock *mockDomainInfoProvider) EntraDomain(context.Context) (*string, error) {
	return mock.entraDomain, mock.entraDomainErr
}

type mockFileSystem struct {
	executableFunc      func() (string, error)
	readFileFunc        func(name string) ([]byte, error)
	writeFileFunc       func(name string, data []byte, perm os.FileMode) error
	mkdirAllFunc        func(path string) error
	removeAllFunc       func(path string) error
	renameFunc          func(oldPath string, newPath string) error
	removeFunc          func(name string) error
	executableInUseFunc func(name string) (bool, error)
}

func (m *mockFileSystem) Executable() (string, error) {
	return m.executableFunc()
}

func (m *mockFileSystem) ReadFile(name string) ([]byte, error) {
	return m.readFileFunc(name)
}

func (m *mockFileSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	return m.writeFileFunc(name, data, perm)
}

func (m *mockFileSystem) MkdirAll(path string) error {
	return m.mkdirAllFunc(path)
}

func (m *mockFileSystem) RemoveAll(path string) error {
	return m.removeAllFunc(path)
}

// Rename, Remove and ExecutableInUse default to the behaviour of a filesystem
// where the write commits and no process holds the executable, so tests only
// override them when the case under test is about those.
func (m *mockFileSystem) Rename(oldPath string, newPath string) error {
	if m.renameFunc == nil {
		return nil
	}
	return m.renameFunc(oldPath, newPath)
}

func (m *mockFileSystem) Remove(name string) error {
	if m.removeFunc == nil {
		return nil
	}
	return m.removeFunc(name)
}

func (m *mockFileSystem) ExecutableInUse(name string) (bool, error) {
	if m.executableInUseFunc == nil {
		return false, nil
	}
	return m.executableInUseFunc(name)
}

type mockService struct {
	isActive  bool
	stopErr   error
	deleteErr error
	startErr  error
	// isActiveFunc overrides isActive when a test needs the reported state to
	// change from one observation to the next, as a service that is still
	// shutting down does.
	isActiveFunc func() bool

	stopCalled   bool
	deleteCalled bool
	startCalled  bool
}

func (m *mockService) IsActive() bool {
	if m.isActiveFunc != nil {
		return m.isActiveFunc()
	}
	return m.isActive
}

// Stop mirrors a real service manager: a stop that reports success means the
// service reached Stopped, so it no longer reports itself active.
func (m *mockService) Stop() error {
	m.stopCalled = true
	if m.stopErr == nil {
		m.isActive = false
	}
	return m.stopErr
}

func (m *mockService) Delete() error {
	m.deleteCalled = true
	return m.deleteErr
}

func (m *mockService) Start() error {
	m.startCalled = true
	return m.startErr
}

func (m *mockService) Close() error { return nil }

type mockServiceManager struct {
	openErr       error
	openService   service.Service
	createErr     error
	createService service.Service

	openCalls   int
	createCalls []service.AgentParams
}

func (m *mockServiceManager) Open(name string) (service.Service, error) {
	m.openCalls++
	if svc, ok := m.openService.(*mockService); ok && svc.deleteCalled {
		// A deleted registration is no longer visible to the manager. That is the
		// signal the deregistration wait polls for, so the mock has to reproduce it
		// or the wait would have nothing to observe.
		return nil, errors.New("service does not exist")
	}
	return m.openService, m.openErr
}

func (m *mockServiceManager) Create(params service.AgentParams) (service.Service, error) {
	m.createCalls = append(m.createCalls, params)
	return m.createService, m.createErr
}
