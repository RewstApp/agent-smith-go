package main

import (
	"strings"
	"testing"
)

func TestNewUpdateContext(t *testing.T) {
	orgId := "test123"

	result, _ := newUpdateContext([]string{"--org-id", orgId, "--update"}, nil, nil, nil, nil)

	if result.OrgId != orgId {
		t.Errorf("expected %v, got %v", orgId, result.OrgId)
	}

	if !result.Update {
		t.Errorf("expected true, got false")
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

	resultWithQos, _ := newUpdateContext(
		[]string{"--org-id", orgId, "--update", "--mqtt-qos", "0"},
		nil, nil, nil, nil,
	)
	if resultWithQos.MqttQos != 0 {
		t.Errorf("expected MqttQos 0, got %v", resultWithQos.MqttQos)
	}

	errorTests := []struct {
		args    []string
		message string
	}{
		{[]string{"--org-id", orgId}, "missing update"},
		{[]string{"--update"}, "missing org-id"},
		{[]string{"--=update"}, "bad flag syntax"},
		{
			[]string{"--org-id", orgId, "--update", "--logging-level", "invalid"},
			"invalid logging-level",
		},
		{
			[]string{"--org-id", orgId, "--update", "--mqtt-qos", "3"},
			"invalid mqtt-qos",
		},
	}

	for _, errorTest := range errorTests {
		_, err := newUpdateContext(errorTest.args, nil, nil, nil, nil)

		if err == nil || !strings.Contains(err.Error(), errorTest.message) {
			t.Errorf("expected error %s, got %v", errorTest.message, err.Error())
		}
	}
}
