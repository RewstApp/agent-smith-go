package main

import (
	"context"
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
