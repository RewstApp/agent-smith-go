package agent

import (
	"context"
	"testing"

	"github.com/hashicorp/go-hclog"
)

func TestGetServiceExecutablePath(t *testing.T) {
	orgId := "org123"
	expected := GetAgentExecutablePath(orgId)

	result := GetServiceExecutablePath(orgId)

	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}
}

func TestGetServiceManagerPath(t *testing.T) {
	orgId := "org123"
	expected := GetAgentExecutablePath(orgId)

	result := GetServiceManagerPath(orgId)

	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}
}

func TestNewPathsData(t *testing.T) {
	orgId := "org123"
	logger := hclog.NewNullLogger()
	sys := &mockSystemInfoProvider{}
	domain := &mockDomainInfoProvider{}

	result, err := NewPathsData(context.Background(), orgId, logger, sys, domain)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if result == nil {
		t.Errorf("expected not nil")
	}
}
