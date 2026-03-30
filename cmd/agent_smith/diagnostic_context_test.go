package main

import (
	"testing"
)

func TestNewDiagnosticContext_Success(t *testing.T) {
	_, err := newDiagnosticContext(
		[]string{"--diagnostic"},
		&mockSystemInfoProvider{},
		&mockDomainInfoProvider{},
		&mockServiceManager{},
		&mockFileSystem{},
	)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
}

func TestNewDiagnosticContext_WithOrgId(t *testing.T) {
	ctx, err := newDiagnosticContext(
		[]string{"--org-id", "test-org", "--diagnostic"},
		&mockSystemInfoProvider{},
		&mockDomainInfoProvider{},
		&mockServiceManager{},
		&mockFileSystem{},
	)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if ctx.OrgId != "test-org" {
		t.Errorf("expected OrgId 'test-org', got %q", ctx.OrgId)
	}
}

func TestNewDiagnosticContext_MissingFlag(t *testing.T) {
	_, err := newDiagnosticContext(
		[]string{"--org-id", "test-org"},
		&mockSystemInfoProvider{},
		&mockDomainInfoProvider{},
		&mockServiceManager{},
		&mockFileSystem{},
	)
	if err == nil {
		t.Fatal("expected error for missing --diagnostic flag, got nil")
	}
}

func TestNewDiagnosticContext_NoArgs(t *testing.T) {
	_, err := newDiagnosticContext(
		[]string{},
		&mockSystemInfoProvider{},
		&mockDomainInfoProvider{},
		&mockServiceManager{},
		&mockFileSystem{},
	)
	if err == nil {
		t.Fatal("expected error for empty args, got nil")
	}
}

func TestNewDiagnosticContext_UnknownFlag(t *testing.T) {
	_, err := newDiagnosticContext(
		[]string{"--diagnostic", "--unknown-flag"},
		&mockSystemInfoProvider{},
		&mockDomainInfoProvider{},
		&mockServiceManager{},
		&mockFileSystem{},
	)
	if err == nil {
		t.Fatal("expected error for unknown flag, got nil")
	}
}
