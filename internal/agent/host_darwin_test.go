//go:build darwin

package agent

import (
	"context"
	"testing"
)

func TestGetAdDomain(t *testing.T) {
	ctx := context.Background()

	result, err := getAdDomain(ctx)

	if result != nil {
		t.Errorf("expected nil, got %v", result)
	}

	if err != nil {
		t.Errorf("expected no error, got %v", err.Error())
	}
}

func TestGetIsAdDomainController(t *testing.T) {
	ctx := context.Background()

	result, err := getIsAdDomainController(ctx)

	if result != false {
		t.Error("expected false, got true")
	}

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

func TestGetIsEntraConnectServer(t *testing.T) {
	result, err := getIsEntraConnectServer()

	if result != false {
		t.Error("expected false, got true")
	}

	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
}

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

func TestGetEntraDomain(t *testing.T) {
	ctx := context.Background()

	result, err := getEntraDomain(ctx)

	if result != nil {
		t.Fatalf("expected nil, got %v", result)
	}

	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}
