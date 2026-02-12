package agent

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/go-hclog"
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

func TestNewHostInfo(t *testing.T) {
	hostInfo := &HostInfo{}
	orgId := "test123"
	logger := hclog.NewNullLogger()
	const ramGb uint64 = 3
	sys := &mockSystemInfoProvider{
		hostname:            "mock",
		hostnameErr:         nil,
		hostPlatform:        "test",
		hostPlatformErr:     nil,
		cpuModelName:        "fake",
		cpuModelNameErr:     nil,
		totalMemoryBytes:    ramGb * 1024 * 1024 * 1024,
		totalMemoryBytesErr: nil,
		macAddress:          nil,
		macAddressErr:       nil,
	}
	domain := &mockDomainInfoProvider{
		adDomain:                nil,
		adDomainErr:             nil,
		isAdDomainController:    true,
		isAdDomainControllerErr: nil,
		isEntraConnectServer:    false,
		isEntraConnectServerErr: nil,
	}

	hostInfo, err := NewHostInfo(context.Background(), orgId, logger, sys, domain)

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if hostInfo.HostName != sys.hostname {
		t.Errorf("expected %v, got %v", sys.hostname, hostInfo.HostName)
	}

	if hostInfo.MacAddress != sys.macAddress {
		t.Errorf("expected %v, got %v", sys.macAddress, hostInfo.MacAddress)
	}

	if hostInfo.OperatingSystem != sys.hostPlatform {
		t.Errorf("expected %v, got %v", sys.hostPlatform, hostInfo.OperatingSystem)
	}

	if hostInfo.CpuModel != sys.cpuModelName {
		t.Errorf("expected %v, got %v", sys.cpuModelName, hostInfo.CpuModel)
	}

	if hostInfo.RamGb != fmt.Sprintf("%d", ramGb) {
		t.Errorf("expected %v, got %v", ramGb, hostInfo.RamGb)
	}

	if hostInfo.AdDomain != domain.adDomain {
		t.Errorf("expected %v, got %v", domain.adDomain, hostInfo.AdDomain)
	}

	if hostInfo.IsAdDomainController != domain.isAdDomainController {
		t.Errorf("expected %v, got %v", domain.isAdDomainController, hostInfo.IsAdDomainController)
	}

	if hostInfo.IsEntraConnectServer != domain.isEntraConnectServer {
		t.Errorf("expected %v, got %v", domain.isEntraConnectServer, hostInfo.IsEntraConnectServer)
	}

	if hostInfo.EntraDomain != domain.entraDomain {
		t.Errorf("expected %v, got %v", domain.entraDomain, hostInfo.EntraDomain)
	}

	if hostInfo.OrgId != orgId {
		t.Errorf("expected %v, got %v", orgId, hostInfo.OrgId)
	}

	// Error paths
	sys.hostPlatformErr = fmt.Errorf("hostname error")
	_, err = NewHostInfo(context.Background(), orgId, logger, sys, domain)

	if err == nil {
		t.Errorf("expected error, got none")
	}

	if err != sys.hostPlatformErr {
		t.Errorf("expected %v, got %v", sys.hostPlatformErr, err)
	}

	sys.hostPlatformErr = nil
	sys.cpuModelNameErr = fmt.Errorf("cpu model name error")

	_, err = NewHostInfo(context.Background(), orgId, logger, sys, domain)

	if err == nil {
		t.Errorf("expected error, got none")
	}

	if err != sys.cpuModelNameErr {
		t.Errorf("expected %v, got %v", sys.cpuModelNameErr, err)
	}

	sys.cpuModelNameErr = nil
	sys.hostnameErr = fmt.Errorf("hostname error")

	_, err = NewHostInfo(context.Background(), orgId, logger, sys, domain)

	if err == nil {
		t.Errorf("expected error, got none")
	}

	if err != sys.hostnameErr {
		t.Errorf("expected %v, got %v", sys.hostnameErr, err)
	}

	sys.hostnameErr = nil
	sys.macAddressErr = fmt.Errorf("mac address error")

	_, err = NewHostInfo(context.Background(), orgId, logger, sys, domain)

	if err == nil {
		t.Errorf("expected error, got none")
	}

	if err != sys.macAddressErr {
		t.Errorf("expected %v, got %v", sys.macAddressErr, err)
	}

	sys.macAddressErr = nil
	sys.totalMemoryBytesErr = fmt.Errorf("total memory bytes error")

	_, err = NewHostInfo(context.Background(), orgId, logger, sys, domain)

	if err == nil {
		t.Errorf("expected error, got none")
	}

	if err != sys.totalMemoryBytesErr {
		t.Errorf("expected %v, got %v", sys.totalMemoryBytesErr, err)
	}

	// Warning paths
	var buf bytes.Buffer
	warningLogger := hclog.New(&hclog.LoggerOptions{
		Output: &buf,
		Level:  hclog.Warn,
	})

	sys.totalMemoryBytesErr = nil
	domain.adDomainErr = fmt.Errorf("ad domain error")

	_, err = NewHostInfo(context.Background(), orgId, warningLogger, sys, domain)

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "AD Domain") {
		t.Errorf("expected AD Domain warning, got %v", output)
	}

	buf.Reset()
	domain.adDomainErr = nil
	domain.isAdDomainControllerErr = fmt.Errorf("is ad domain controller error")

	_, err = NewHostInfo(context.Background(), orgId, warningLogger, sys, domain)

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	output = buf.String()
	if !strings.Contains(output, "AD Domain Controller") {
		t.Errorf("expected AD Domain Controller warning, got %v", output)
	}

	buf.Reset()
	domain.isAdDomainControllerErr = nil
	domain.isEntraConnectServerErr = fmt.Errorf("is entra connect server error")

	_, err = NewHostInfo(context.Background(), orgId, warningLogger, sys, domain)

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	output = buf.String()
	if !strings.Contains(output, "Entra Connect Server") {
		t.Errorf("expected AD Domain Controller warning, got %v", output)
	}

	buf.Reset()
	domain.isEntraConnectServerErr = nil
	domain.entraDomainErr = fmt.Errorf("entra domain error")

	_, err = NewHostInfo(context.Background(), orgId, warningLogger, sys, domain)

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	output = buf.String()
	if !strings.Contains(output, "Entra Domain") {
		t.Errorf("expected Entra Domain warnsing, got %v", output)
	}
}
