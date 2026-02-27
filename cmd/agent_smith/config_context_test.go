package main

import (
	"strings"
	"testing"
)

func TestNewConfigContext(t *testing.T) {
	orgId := "test123"
	configUrl := "https://config.url/"
	configSecret := "secret123"

	result, _ := newConfigContext([]string{"--org-id", orgId, "--config-url", configUrl, "--config-secret", configSecret}, nil, nil, nil, nil)

	if result.OrgId != orgId {
		t.Errorf("expected %v, got %v", orgId, result.OrgId)
	}

	if result.ConfigUrl != configUrl {
		t.Errorf("expected %v, got %v", configUrl, result.ConfigUrl)
	}

	if result.ConfigSecret != configSecret {
		t.Errorf("expected %v, got %v", configSecret, result.ConfigSecret)
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
		{[]string{"--config-url", configUrl, "--config-secret", configSecret}, "missing org-id"},
		{[]string{"--org-id", orgId, "--config-secret", configSecret}, "missing config-url"},
		{[]string{"--org-id", orgId, "--config-url", configUrl}, "missing config-secret"},
		{[]string{"--=uninstall"}, "bad flag syntax"},
		{[]string{"--org-id", orgId, "--config-url", configUrl, "--config-secret", configSecret, "--logging-level", "invalid"}, "invalid logging-level"},
	}

	for _, errorTest := range errorTests {
		_, err := newConfigContext(errorTest.args, nil, nil, nil, nil)

		if err == nil || !strings.Contains(err.Error(), errorTest.message) {
			t.Errorf("expected error %s, got %v", errorTest.message, err.Error())
		}
	}
}
