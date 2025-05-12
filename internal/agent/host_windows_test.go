//go:build windows

package agent

import (
	"testing"
)

func TestGetMacAddress(t *testing.T) {
	mac, err := getMacAddress()
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if mac == nil || len(*mac) == 0 {
		t.Fatal("Expected a valid MAC address, got nil or empty")
	}
	if len(*mac) != 12 { // Without colons, MAC address is 12 hex chars
		t.Errorf("Expected 12-character MAC, got: %s", *mac)
	}
}
