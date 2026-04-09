//go:build linux || darwin

package service

import (
	"testing"
)

type immediateRunner struct {
	exitCode ServiceExitCode
}

func (r *immediateRunner) Name() string { return "test" }
func (r *immediateRunner) Execute(stop <-chan struct{}, running chan<- struct{}) ServiceExitCode {
	running <- struct{}{}
	return r.exitCode
}

func TestRun_ReturnsZeroExitCode(t *testing.T) {
	code, err := Run(&immediateRunner{exitCode: 0})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if code != 0 {
		t.Errorf("expected exit code 0, got %d", code)
	}
}

func TestRun_ReturnsNonZeroExitCode(t *testing.T) {
	code, err := Run(&immediateRunner{exitCode: GenericError})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if code != int(GenericError) {
		t.Errorf("expected exit code %d, got %d", GenericError, code)
	}
}
