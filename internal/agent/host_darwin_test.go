//go:build darwin

package agent

import (
	"context"
	"os"
	"testing"
)

func TestDarwinDefaultSystemInfoProvider_MACAddress(t *testing.T) {
	sys := &darwinDefaultSystemInfoProvider{}
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

func TestDarwinDefaultSystemInfoProvider_Hostname(t *testing.T) {
	sys := &darwinDefaultSystemInfoProvider{}
	expected, _ := os.Hostname()

	result, err := sys.Hostname()
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if expected != result {
		t.Errorf("expected %s, got %s", expected, result)
	}
}

func TestDarwinDefaultSystemInfoProvider_HostPlatform(t *testing.T) {
	sys := &darwinDefaultSystemInfoProvider{}

	_, err := sys.HostPlatform()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestDarwinDefaultSystemInfoProvider_CPUModelName(t *testing.T) {
	sys := &darwinDefaultSystemInfoProvider{}
	_, err := sys.CPUModelName()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestDarwinDefaultSystemInfoProvider_TotalMemoryBytes(t *testing.T) {
	sys := &darwinDefaultSystemInfoProvider{}
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
	_, ok := result.(*darwinDefaultSystemInfoProvider)

	if !ok {
		t.Errorf("expected *darwinDefaultSystemInfoProvider, got %T", result)
	}
}

func TestDarwinDefaultDomainInfoProvider_ADDomain(t *testing.T) {
	domain := &darwinDefaultDomainInfoProvider{}
	_, err := domain.ADDomain(context.Background())
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestDarwinDefaultDomainInfoProvider_IsADDomainController(t *testing.T) {
	domain := &darwinDefaultDomainInfoProvider{}
	_, err := domain.IsADDomainController(context.Background())
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestDarwinDefaultDomainInfoProvider_IsEntraConnectServer(t *testing.T) {
	domain := &darwinDefaultDomainInfoProvider{}
	_, err := domain.IsEntraConnectServer()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestDarwinDefaultDomainInfoProvider_EntraDomain(t *testing.T) {
	domain := &darwinDefaultDomainInfoProvider{}
	_, err := domain.EntraDomain(context.Background())
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestNewDomainInfoProvider(t *testing.T) {
	result := NewDomainInfoProvider()
	_, ok := result.(*darwinDefaultDomainInfoProvider)

	if !ok {
		t.Errorf("expected *darwinDefaultDomainInfoProvider, got %T", result)
	}
}
