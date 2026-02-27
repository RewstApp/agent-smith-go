package main

import (
	"strings"
	"testing"
)

func TestNewUpdateContext(t *testing.T) {
	orgId := "test123"

	result, _ := newUpdateContext([]string{"--org-id", orgId, "--update"}, nil, nil, nil)

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

	errorTests := []struct {
		args    []string
		message string
	}{
		{[]string{"--org-id", orgId}, "missing update"},
		{[]string{"--update"}, "missing org-id"},
		{[]string{"--=update"}, "bad flag syntax"},
		{[]string{"--org-id", orgId, "--update", "--logging-level", "invalid"}, "invalid logging-level"},
	}

	for _, errorTest := range errorTests {
		_, err := newUpdateContext(errorTest.args, nil, nil, nil)

		if err == nil || !strings.Contains(err.Error(), errorTest.message) {
			t.Errorf("expected error %s, got %v", errorTest.message, err.Error())
		}
	}
}
