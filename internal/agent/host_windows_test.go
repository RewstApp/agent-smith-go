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
	domain := &windowsDefaultDomainInfoProvider{psRunner: defaultPSRunner}
	_, err := domain.ADDomain(context.Background())
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestWindowsDefaultDomainInfoProvider_ADDomain_CleanOutput(t *testing.T) {
	provider := &windowsDefaultDomainInfoProvider{
		psRunner: func(_ context.Context, _ string) (string, error) {
			return "example.com", nil
		},
	}
	result, err := provider.ADDomain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil || *result != "example.com" {
		t.Errorf("expected example.com, got %v", result)
	}
}

func TestWindowsDefaultDomainInfoProvider_ADDomain_EmptyOutput(t *testing.T) {
	provider := &windowsDefaultDomainInfoProvider{
		psRunner: func(_ context.Context, _ string) (string, error) {
			return "", nil
		},
	}
	result, err := provider.ADDomain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != nil {
		t.Errorf("expected nil for workgroup machine, got %v", *result)
	}
}

// TestWindowsDefaultDomainInfoProvider_ADDomain_ProfileNoise documents the contamination
// bug: without -NoProfile, profile stdout prepends noise to the domain, so the returned
// value is the full noisy string instead of just the domain name.
func TestWindowsDefaultDomainInfoProvider_ADDomain_ProfileNoise(t *testing.T) {
	provider := &windowsDefaultDomainInfoProvider{
		psRunner: func(_ context.Context, _ string) (string, error) {
			// Simulate profile output prepended before the actual domain (no -NoProfile).
			return "Welcome to PowerShell!\nexample.com", nil
		},
	}
	result, err := provider.ADDomain(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected a result, got nil")
	}
	// Without -NoProfile the full contaminated string is returned, not just the domain.
	if *result == "example.com" {
		t.Error(
			"profile noise corrupts domain value; -NoProfile suppresses this in production",
		)
	}
}

func TestWindowsDefaultDomainInfoProvider_IsADDomainController(t *testing.T) {
	domain := &windowsDefaultDomainInfoProvider{psRunner: defaultPSRunner}
	_, err := domain.IsADDomainController(context.Background())
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestWindowsDefaultDomainInfoProvider_IsADDomainController_True(t *testing.T) {
	provider := &windowsDefaultDomainInfoProvider{
		psRunner: func(_ context.Context, _ string) (string, error) {
			return "True", nil
		},
	}
	result, err := provider.IsADDomainController(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result {
		t.Error("expected true, got false")
	}
}

func TestWindowsDefaultDomainInfoProvider_IsADDomainController_False(t *testing.T) {
	provider := &windowsDefaultDomainInfoProvider{
		psRunner: func(_ context.Context, _ string) (string, error) {
			return "False", nil
		},
	}
	result, err := provider.IsADDomainController(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result {
		t.Error("expected false, got true")
	}
}

// TestWindowsDefaultDomainInfoProvider_IsADDomainController_ProfileNoise documents that
// without -NoProfile, profile stdout causes the "True"/"False" comparison to fail, always
// returning false even for actual domain controllers.
func TestWindowsDefaultDomainInfoProvider_IsADDomainController_ProfileNoise(t *testing.T) {
	provider := &windowsDefaultDomainInfoProvider{
		psRunner: func(_ context.Context, _ string) (string, error) {
			// Simulate profile noise before "True" (no -NoProfile).
			return "Welcome to PowerShell!\nTrue", nil
		},
	}
	result, err := provider.IsADDomainController(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Without -NoProfile the comparison fails and a DC is reported as non-DC.
	if result {
		t.Error(
			"profile noise suppresses DC true result; -NoProfile suppresses this in production",
		)
	}
}

func TestWindowsDefaultDomainInfoProvider_IsEntraConnectServer(t *testing.T) {
	domain := &windowsDefaultDomainInfoProvider{psRunner: defaultPSRunner}
	_, err := domain.IsEntraConnectServer()
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestWindowsDefaultDomainInfoProvider_EntraDomain(t *testing.T) {
	domain := &windowsDefaultDomainInfoProvider{psRunner: defaultPSRunner}
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
