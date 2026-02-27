package main

import (
	"strings"
	"testing"
)

func TestNewUninstallContext(t *testing.T) {
	orgId := "test123"
	result, _ := newUninstallContext([]string{"--org-id", orgId, "--uninstall"}, nil)

	if result.OrgId != orgId {
		t.Errorf("expected %v, got %v", orgId, result.OrgId)
	}

	if !result.Uninstall {
		t.Errorf("expected true, got false")
	}

	errorTests := []struct {
		args    []string
		message string
	}{
		{[]string{"--org-id", orgId}, "missing uninstall"},
		{[]string{"--uninstall"}, "missing org-id"},
		{[]string{"--=uninstall"}, "bad flag syntax"},
	}

	for _, errorTest := range errorTests {
		_, err := newUninstallContext(errorTest.args, nil)

		if err == nil || !strings.Contains(err.Error(), errorTest.message) {
			t.Errorf("expected error %s, got %v", errorTest.message, err.Error())
		}
	}
}
