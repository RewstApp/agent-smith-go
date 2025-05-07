package main

import (
	"strings"
	"testing"
)

func TestParseUninstallParams(t *testing.T) {
	orgId := "test123"
	result, _ := parseUninstallParams([]string{"--org-id", orgId, "--uninstall"})

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
		_, err := parseUninstallParams(errorTest.args)

		if err == nil || !strings.Contains(err.Error(), errorTest.message) {
			t.Errorf("expected error %s, got %v", errorTest.message, err.Error())
		}
	}
}

func TestParseConfigParams(t *testing.T) {
	orgId := "test123"
	configUrl := "https://config.url/"
	configSecret := "secret123"

	result, _ := parseConfigParams([]string{"--org-id", orgId, "--config-url", configUrl, "--config-secret", configSecret})

	if result.OrgId != orgId {
		t.Errorf("expected %v, got %v", orgId, result.OrgId)
	}

	if result.ConfigUrl != configUrl {
		t.Errorf("expected %v, got %v", configUrl, result.ConfigUrl)
	}

	if result.ConfigSecret != configSecret {
		t.Errorf("expected %v, got %v", configSecret, result.ConfigSecret)
	}

	errorTests := []struct {
		args    []string
		message string
	}{
		{[]string{"--config-url", configUrl, "--config-secret", configSecret}, "missing org-id"},
		{[]string{"--org-id", orgId, "--config-secret", configSecret}, "missing config-url"},
		{[]string{"--org-id", orgId, "--config-url", configUrl}, "missing config-secret"},
		{[]string{"--=uninstall"}, "bad flag syntax"},
	}

	for _, errorTest := range errorTests {
		_, err := parseConfigParams(errorTest.args)

		if err == nil || !strings.Contains(err.Error(), errorTest.message) {
			t.Errorf("expected error %s, got %v", errorTest.message, err.Error())
		}
	}
}

func TestParseServiceParams(t *testing.T) {
	orgId := "test123"
	configFile := "/file/config"
	logFile := "/file/log"

	result, _ := parseServiceParams([]string{"--org-id", orgId, "--config-file", configFile, "--log-file", logFile})

	if result.OrgId != orgId {
		t.Errorf("expected %v, got %v", orgId, result.OrgId)
	}

	if result.ConfigFile != configFile {
		t.Errorf("expected %v, got %v", configFile, result.ConfigFile)
	}

	if result.LogFile != logFile {
		t.Errorf("expected %v, got %v", logFile, result.LogFile)
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
		_, err := parseServiceParams(errorTest.args)

		if err == nil || !strings.Contains(err.Error(), errorTest.message) {
			t.Errorf("expected error %s, got %v", errorTest.message, err.Error())
		}
	}
}
