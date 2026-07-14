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

	if result.ServiceUsername != "" {
		t.Errorf("expected empty ServiceUsername by default, got %q", result.ServiceUsername)
	}
	if result.ServicePassword != "" {
		t.Errorf("expected empty ServicePassword by default, got %q", result.ServicePassword)
	}

	resultWithCreds, err := newUpdateContext(
		[]string{
			"--org-id", orgId, "--update",
			"--service-username", "rewst",
			"--service-password", "p@ss",
		},
		nil, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("expected no error with service credentials, got %v", err)
	}
	if resultWithCreds.ServiceUsername != "rewst" {
		t.Errorf("expected ServiceUsername %q, got %q", "rewst", resultWithCreds.ServiceUsername)
	}
	if resultWithCreds.ServicePassword != "p@ss" {
		t.Errorf("expected ServicePassword %q, got %q", "p@ss", resultWithCreds.ServicePassword)
	}

	// Tuning flags default to the unset sentinel when omitted.
	if result.Tuning.MqttConnectTimeoutSeconds != tuningFlagUnset ||
		result.Tuning.WorkerCount != tuningFlagUnset ||
		result.Tuning.MessageQueueSize != tuningFlagUnset ||
		result.Tuning.PostbackMaxAttempts != tuningFlagUnset ||
		result.Tuning.PostbackBaseRetryBackoffSeconds != tuningFlagUnset {
		t.Errorf("expected tuning flags to default to unset, got %+v", result.Tuning)
	}

	// Tuning flags are parsed when provided.
	resultWithTuning, err := newUpdateContext(
		[]string{
			"--org-id", orgId, "--update",
			"--mqtt-connect-timeout-seconds", "45",
			"--worker-count", "20",
			"--message-queue-size", "250",
			"--postback-max-attempts", "5",
			"--postback-base-retry-backoff-seconds", "2",
		},
		nil, nil, nil, nil,
	)
	if err != nil {
		t.Fatalf("expected no error with tuning flags, got %v", err)
	}
	if resultWithTuning.Tuning.MqttConnectTimeoutSeconds != 45 {
		t.Errorf("expected MqttConnectTimeoutSeconds 45, got %v", resultWithTuning.Tuning.MqttConnectTimeoutSeconds)
	}
	if resultWithTuning.Tuning.WorkerCount != 20 {
		t.Errorf("expected WorkerCount 20, got %v", resultWithTuning.Tuning.WorkerCount)
	}
	if resultWithTuning.Tuning.MessageQueueSize != 250 {
		t.Errorf("expected MessageQueueSize 250, got %v", resultWithTuning.Tuning.MessageQueueSize)
	}
	if resultWithTuning.Tuning.PostbackMaxAttempts != 5 {
		t.Errorf("expected PostbackMaxAttempts 5, got %v", resultWithTuning.Tuning.PostbackMaxAttempts)
	}
	if resultWithTuning.Tuning.PostbackBaseRetryBackoffSeconds != 2 {
		t.Errorf("expected PostbackBaseRetryBackoffSeconds 2, got %v", resultWithTuning.Tuning.PostbackBaseRetryBackoffSeconds)
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
			[]string{"--org-id", orgId, "--update", "--mqtt-qos", "2"},
			"invalid mqtt-qos",
		},
		{
			[]string{"--org-id", orgId, "--update", "--service-password", "p@ss"},
			"service-password requires service-username",
		},
		{
			[]string{"--org-id", orgId, "--update", "--worker-count", "-1"},
			"invalid worker-count: must be a positive integer",
		},
		{
			[]string{"--org-id", orgId, "--update", "--message-queue-size", "0"},
			"invalid message-queue-size: must be a positive integer",
		},
		{
			[]string{"--org-id", orgId, "--update", "--postback-max-attempts", "abc"},
			"invalid value",
		},
		{
			[]string{"--org-id", orgId, "--update", "--mqtt-connect-timeout-seconds", "0"},
			"invalid mqtt-connect-timeout-seconds: must be a positive integer",
		},
		{
			[]string{"--org-id", orgId, "--update", "--postback-base-retry-backoff-seconds", "-5"},
			"invalid postback-base-retry-backoff-seconds: must be a positive integer",
		},
	}

	for _, errorTest := range errorTests {
		_, err := newUpdateContext(errorTest.args, nil, nil, nil, nil)

		if err == nil || !strings.Contains(err.Error(), errorTest.message) {
			t.Errorf("expected error %s, got %v", errorTest.message, err.Error())
		}
	}
}
