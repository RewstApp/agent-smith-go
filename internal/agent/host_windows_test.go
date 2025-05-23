//go:build windows

package agent

import (
	"testing"
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
