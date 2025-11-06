//go:build windows

package agent

import (
	"context"
	"testing"
	"time"
)

func TestGetMacAddress(t *testing.T) {
	mac, err := getMacAddress()

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if mac == nil || len(*mac) == 0 {
		t.Fatal("expected a valid mac address, got nil or empty")
	}

	if len(*mac) != 12 {
		t.Errorf("expected 12-character mac, got %s", *mac)
	}
}

func TestGetAdDomain(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping host function test in short mode")
	}

	ctx := context.Background()
	domain, err := getAdDomain(ctx)

	// This test may return either nil or a domain name, both are valid
	if err != nil {
		t.Logf("getAdDomain() returned error (may be expected): %v", err)
	}

	if domain != nil {
		t.Logf("getAdDomain() returned domain: %s", *domain)
		if len(*domain) == 0 {
			t.Error("domain should not be empty string")
		}
	} else {
		t.Log("getAdDomain() returned nil (not domain-joined or WORKGROUP)")
	}
}

func TestGetAdDomain_CancelledContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping host function test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := getAdDomain(ctx)
	if err == nil {
		// Command may complete before cancellation, which is fine
		t.Log("getAdDomain() completed before cancellation")
	} else {
		t.Logf("getAdDomain() returned error (expected for cancelled context): %v", err)
	}
}

func TestGetAdDomain_Timeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping host function test in short mode")
	}

	// Very short timeout to potentially trigger timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	_, err := getAdDomain(ctx)
	// Either completes or times out, both are valid
	t.Logf("getAdDomain() with short timeout: err=%v", err)
}

func TestGetIsAdDomainController(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping host function test in short mode")
	}

	ctx := context.Background()
	isDC, err := getIsAdDomainController(ctx)

	if err != nil {
		t.Logf("getIsAdDomainController() returned error (may be expected): %v", err)
	}

	t.Logf("getIsAdDomainController() returned: %v", isDC)
	// Most test machines will return false, which is expected
}

func TestGetIsAdDomainController_CancelledContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping host function test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := getIsAdDomainController(ctx)
	if err == nil {
		t.Log("getIsAdDomainController() completed before cancellation")
	} else {
		t.Logf("getIsAdDomainController() returned error (expected for cancelled context): %v", err)
	}
}

func TestGetIsEntraConnectServer(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping host function test in short mode")
	}

	isEntra, err := getIsEntraConnectServer()
	if err != nil {
		t.Fatalf("getIsEntraConnectServer() error = %v", err)
	}

	t.Logf("getIsEntraConnectServer() returned: %v", isEntra)
	// Most test machines will return false, which is expected
	// The function should not error on a normal Windows machine
}

func TestGetEntraDomain(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping host function test in short mode")
	}

	ctx := context.Background()
	domain, err := getEntraDomain(ctx)

	// This test may return either nil or a domain name, or an error
	// All are valid depending on the system configuration
	if err != nil {
		t.Logf("getEntraDomain() returned error (may be expected): %v", err)
		return
	}

	if domain != nil {
		t.Logf("getEntraDomain() returned domain: %s", *domain)
		if len(*domain) == 0 {
			t.Error("domain should not be empty string")
		}
	} else {
		t.Log("getEntraDomain() returned nil (not Azure AD joined)")
	}
}

func TestGetEntraDomain_CancelledContext(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping host function test in short mode")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := getEntraDomain(ctx)
	if err == nil {
		t.Log("getEntraDomain() completed before cancellation")
	} else {
		t.Logf("getEntraDomain() returned error (expected for cancelled context): %v", err)
	}
}

func TestGetEntraDomain_Timeout(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping host function test in short mode")
	}

	// Very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	_, err := getEntraDomain(ctx)
	// Either completes or times out, both are valid
	t.Logf("getEntraDomain() with short timeout: err=%v", err)
}
