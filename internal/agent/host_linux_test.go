//go:build linux

package agent

import (
	"context"
	"errors"
	"net"
	"os"
	"testing"
)

func TestLinuxDefaultSystemInfoProvider_MACAddress_NoInterface(t *testing.T) {
	orig := netInterfaces
	netInterfaces = func() ([]net.Interface, error) { return nil, nil }
	defer func() { netInterfaces = orig }()

	sys := &linuxDefaultSystemInfoProvider{}
	_, err := sys.MACAddress()
	if !errors.Is(err, ErrNoMACAddress) {
		t.Errorf("expected ErrNoMACAddress, got %v", err)
	}
}

func TestLinuxDefaultSystemInfoProvider_MACAddress(t *testing.T) {
	sys := &linuxDefaultSystemInfoProvider{}
	result, err := sys.MACAddress()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if result == nil || len(*result) == 0 {
		t.Fatal("expected a valid mac address, got nil or empty")
	}

	if len(*result) != 12 {
		t.Errorf("expected 12-character mac, got %s", *result)
	}
}

func TestLinuxDefaultSystemInfoProvider_Hostname(t *testing.T) {
	sys := &linuxDefaultSystemInfoProvider{}
	expected, _ := os.Hostname()

	result, err := sys.Hostname()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if expected != result {
		t.Errorf("expected %s, got %s", expected, result)
	}
}

func TestLinuxDefaultSystemInfoProvider_HostPlatform(t *testing.T) {
	sys := &linuxDefaultSystemInfoProvider{}

	_, err := sys.HostPlatform()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestLinuxDefaultSystemInfoProvider_CPUModelName(t *testing.T) {
	sys := &linuxDefaultSystemInfoProvider{}
	_, err := sys.CPUModelName()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestLinuxDefaultSystemInfoProvider_TotalMemoryBytes(t *testing.T) {
	sys := &linuxDefaultSystemInfoProvider{}
	result, err := sys.TotalMemoryBytes()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if result == 0 {
		t.Errorf("expected positive, got %v", result)
	}
}

func TestNewSystemInfoProvider(t *testing.T) {
	result := NewSystemInfoProvider()
	_, ok := result.(*linuxDefaultSystemInfoProvider)

	if !ok {
		t.Errorf("expected *linuxDefaultSystemInfoProvider, got %T", result)
	}
}

func TestLinuxDefaultDomainInfoProvider_ADDomain(t *testing.T) {
	domain := &linuxDefaultDomainInfoProvider{}
	_, err := domain.ADDomain(context.Background())
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestLinuxDefaultDomainInfoProvider_IsADDomainController(t *testing.T) {
	domain := &linuxDefaultDomainInfoProvider{}
	_, err := domain.IsADDomainController(context.Background())
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestLinuxDefaultDomainInfoProvider_IsEntraConnectServer(t *testing.T) {
	domain := &linuxDefaultDomainInfoProvider{}
	_, err := domain.IsEntraConnectServer()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestLinuxDefaultDomainInfoProvider_EntraDomain(t *testing.T) {
	domain := &linuxDefaultDomainInfoProvider{}
	_, err := domain.EntraDomain(context.Background())
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestNewDomainInfoProvider(t *testing.T) {
	result := NewDomainInfoProvider()
	_, ok := result.(*linuxDefaultDomainInfoProvider)

	if !ok {
		t.Errorf("expected *linuxDefaultDomainInfoProvider, got %T", result)
	}
}
