package main

import (
	"strings"
	"testing"
)

func TestNewServiceContext(t *testing.T) {
	orgId := "test123"
	configFile := "/file/config"
	logFile := "/file/log"

	result, _ := newServiceContext(
		[]string{"--org-id", orgId, "--config-file", configFile, "--log-file", logFile},
		nil,
		nil,
		nil,
	)

	if result.OrgId != orgId {
		t.Errorf("expected %v, got %v", orgId, result.OrgId)
	}

	if result.ConfigFile != configFile {
		t.Errorf("expected %v, got %v", configFile, result.ConfigFile)
	}

	if result.LogFile != logFile {
		t.Errorf("expected %v, got %v", logFile, result.LogFile)
	}

	if result.Sys != nil {
		t.Errorf("expected nil, got %v", result.Sys)
	}

	if result.Domain != nil {
		t.Errorf("expected nil, got %v", result.Domain)
	}

	errorTests := []struct {
		args    []string
		message string
	}{
		{[]string{"--config-file", configFile, "--log-file", logFile}, "missing org-id"},
		{[]string{"--org-id", orgId, "--config-file", configFile}, "missing log-file"},
		{[]string{"--org-id", orgId, "--log-file", logFile}, "missing config-file"},
		{[]string{"--=uninstall"}, "bad flag syntax"},
	}

	for _, errorTest := range errorTests {
		_, err := newServiceContext(errorTest.args, nil, nil, nil)

		if err == nil || !strings.Contains(err.Error(), errorTest.message) {
			t.Errorf("expected error %s, got %v", errorTest.message, err.Error())
		}
	}
}
