//go:build windows

package agent

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
)

func TestWindowsDefaultSystemInfoProvider_MACAddress_NoInterface(t *testing.T) {
	orig := netInterfaces
	netInterfaces = func() ([]net.Interface, error) { return nil, nil }
	defer func() { netInterfaces = orig }()

	sys := &windowsDefaultSystemInfoProvider{}
	_, err := sys.MACAddress()
	if !errors.Is(err, ErrNoMACAddress) {
		t.Errorf("expected ErrNoMACAddress, got %v", err)
	}
}

func TestWindowsDefaultSystemInfoProvider_MACAddress(t *testing.T) {
	sys := &windowsDefaultSystemInfoProvider{}
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

func TestWindowsDefaultSystemInfoProvider_Hostname(t *testing.T) {
	sys := &windowsDefaultSystemInfoProvider{}
	expected, _ := os.Hostname()

	result, err := sys.Hostname()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if expected != result {
		t.Errorf("expected %s, got %s", expected, result)
	}
}

func TestWindowsDefaultSystemInfoProvider_HostPlatform(t *testing.T) {
	sys := &windowsDefaultSystemInfoProvider{}
	expected := "windows"

	result, err := sys.HostPlatform()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if !strings.Contains(strings.ToLower(result), expected) {
		t.Errorf("expected to contain %s, got %s", expected, result)
	}
}

func TestWindowsDefaultSystemInfoProvider_CPUModelName(t *testing.T) {
	sys := &windowsDefaultSystemInfoProvider{}
	_, err := sys.CPUModelName()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestWindowsDefaultSystemInfoProvider_TotalMemoryBytes(t *testing.T) {
	sys := &windowsDefaultSystemInfoProvider{}
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
	_, ok := result.(*windowsDefaultSystemInfoProvider)

	if !ok {
		t.Errorf("expected *windowsDefaultSystemInfoProvider, got %T", result)
	}
}

func TestWindowsDefaultDomainInfoProvider_ADDomain(t *testing.T) {
	domain := &windowsDefaultDomainInfoProvider{}
	_, err := domain.ADDomain(context.Background())
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestWindowsDefaultDomainInfoProvider_IsADDomainController(t *testing.T) {
	domain := &windowsDefaultDomainInfoProvider{}
	_, err := domain.IsADDomainController(context.Background())
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestWindowsDefaultDomainInfoProvider_IsEntraConnectServer(t *testing.T) {
	domain := &windowsDefaultDomainInfoProvider{}
	_, err := domain.IsEntraConnectServer()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestWindowsDefaultDomainInfoProvider_EntraDomain(t *testing.T) {
	domain := &windowsDefaultDomainInfoProvider{}
	_, err := domain.EntraDomain(context.Background())
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestNewDomainInfoProvider(t *testing.T) {
	result := NewDomainInfoProvider()
	_, ok := result.(*windowsDefaultDomainInfoProvider)

	if !ok {
		t.Errorf("expected *windowsDefaultDomainInfoProvider, got %T", result)
	}
}
