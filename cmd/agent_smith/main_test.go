package main

import (
	"context"
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
	executableFunc func() (string, error)
	readFileFunc   func(name string) ([]byte, error)
	writeFileFunc  func(name string, data []byte, perm os.FileMode) error
	mkdirAllFunc   func(path string) error
	removeAllFunc  func(path string) error
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

type mockService struct {
	isActive  bool
	stopErr   error
	deleteErr error
	startErr  error
}

func (m *mockService) IsActive() bool { return m.isActive }
func (m *mockService) Stop() error    { return m.stopErr }
func (m *mockService) Delete() error  { return m.deleteErr }
func (m *mockService) Start() error   { return m.startErr }
func (m *mockService) Close() error   { return nil }

type mockServiceManager struct {
	openErr              error
	openService          service.Service
	createErr            error
	createService        service.Service
	capturedCreateParams service.AgentParams
}

func (m *mockServiceManager) Open(name string) (service.Service, error) {
	return m.openService, m.openErr
}

func (m *mockServiceManager) Create(params service.AgentParams) (service.Service, error) {
	m.capturedCreateParams = params
	return m.createService, m.createErr
}
