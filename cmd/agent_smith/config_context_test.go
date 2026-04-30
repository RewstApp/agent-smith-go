package main

import (
	"strings"
	"testing"
)

func TestNewConfigContext(t *testing.T) {
	orgId := "test123"
	configUrl := "https://config.url/"
	configSecret := "secret123"

	result, _ := newConfigContext(
		[]string{"--org-id", orgId, "--config-url", configUrl, "--config-secret", configSecret},
		nil,
		nil,
		nil,
		nil,
	)

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

	if result.MqttQos != -1 {
		t.Errorf("expected MqttQos -1 (unset), got %v", result.MqttQos)
	}

	resultWithQos, _ := newConfigContext(
		[]string{
			"--org-id", orgId,
			"--config-url", configUrl,
			"--config-secret", configSecret,
			"--mqtt-qos", "2",
		},
		nil, nil, nil, nil,
	)
	if resultWithQos.MqttQos != 2 {
		t.Errorf("expected MqttQos 2, got %v", resultWithQos.MqttQos)
	}

	errorTests := []struct {
		args    []string
		message string
	}{
		{[]string{"--config-url", configUrl, "--config-secret", configSecret}, "missing org-id"},
		{[]string{"--org-id", orgId, "--config-secret", configSecret}, "missing config-url"},
		{[]string{"--org-id", orgId, "--config-url", configUrl}, "missing config-secret"},
		{[]string{"--=uninstall"}, "bad flag syntax"},
		{
			[]string{
				"--org-id",
				orgId,
				"--config-url",
				configUrl,
				"--config-secret",
				configSecret,
				"--logging-level",
				"invalid",
			},
			"invalid logging-level",
		},
		{
			[]string{
				"--org-id",
				orgId,
				"--config-url",
				configUrl,
				"--config-secret",
				configSecret,
				"--mqtt-qos",
				"3",
			},
			"invalid mqtt-qos",
		},
	}

	for _, errorTest := range errorTests {
		_, err := newConfigContext(errorTest.args, nil, nil, nil, nil)

		if err == nil || !strings.Contains(err.Error(), errorTest.message) {
			t.Errorf("expected error %s, got %v", errorTest.message, err.Error())
		}
	}
}

func TestNewConfigContext_ServiceCredentials(t *testing.T) {
	result, err := newConfigContext(
		[]string{
			"--org-id", "test123",
			"--config-url", "https://config.url/",
			"--config-secret", "secret123",
			"--service-username", `DOMAIN\svc_rewst`,
			"--service-password", "p@ssw0rd",
		},
		nil, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.ServiceUsername != `DOMAIN\svc_rewst` {
		t.Errorf("expected ServiceUsername 'DOMAIN\\svc_rewst', got %q", result.ServiceUsername)
	}
	if result.ServicePassword != "p@ssw0rd" {
		t.Errorf("expected ServicePassword 'p@ssw0rd', got %q", result.ServicePassword)
	}
}

func TestNewConfigContext_ServiceCredentialsDefaultEmpty(t *testing.T) {
	result, err := newConfigContext(
		[]string{
			"--org-id",
			"test123",
			"--config-url",
			"https://config.url/",
			"--config-secret",
			"secret123",
		},
		nil,
		nil,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if result.ServiceUsername != "" {
		t.Errorf("expected empty ServiceUsername by default, got %q", result.ServiceUsername)
	}
	if result.ServicePassword != "" {
		t.Errorf("expected empty ServicePassword by default, got %q", result.ServicePassword)
	}
}
