package main

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// buildModeErrs runs every mode's context constructor against args and returns
// the resulting error map, mirroring how main collects them. Providers are nil
// because the failure paths under test never reach provider usage.
func buildModeErrs(args []string) map[string]error {
	modeErrs := map[string]error{}

	_, err := newDiagnosticContext(args, nil, nil, nil, nil)
	modeErrs["diagnostic"] = err

	_, err = newUninstallContext(args, nil, nil)
	modeErrs["uninstall"] = err

	_, err = newConfigContext(args, nil, nil, nil, nil)
	modeErrs["config"] = err

	_, err = newServiceContext(args, nil, nil, nil)
	modeErrs["service"] = err

	_, err = newUpdateContext(args, nil, nil, nil, nil)
	modeErrs["update"] = err

	return modeErrs
}

func TestFlagNameFromArg(t *testing.T) {
	tests := []struct {
		arg    string
		want   string
		wantOk bool
	}{
		{"--config-url", "config-url", true},
		{"-h", "h", true},
		{"--mqtt-qos=2", "mqtt-qos", true},
		{"--help", "help", true},
		{"positional", "", false},
		{"-", "", false},
		{"--", "", false},
		{"", "", false},
	}

	for _, test := range tests {
		got, ok := flagNameFromArg(test.arg)
		if got != test.want || ok != test.wantOk {
			t.Errorf("flagNameFromArg(%q) = (%q, %v), want (%q, %v)",
				test.arg, got, ok, test.want, test.wantOk)
		}
	}
}

func TestDetectMode(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantMode string
		wantOk   bool
	}{
		{"diagnostic", []string{"--diagnostic"}, "diagnostic", true},
		{"uninstall", []string{"--org-id", "x", "--uninstall"}, "uninstall", true},
		{"config", []string{"--config-url", "https://x"}, "config", true},
		{"service", []string{"--config-file", "/etc/x"}, "service", true},
		{"update", []string{"--update"}, "update", true},
		{"qos value form", []string{"--config-url=https://x"}, "config", true},
		{"no selector", []string{"--org-id", "x"}, "", false},
		{"empty", []string{}, "", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mode, ok := detectMode(test.args)
			if ok != test.wantOk {
				t.Fatalf("detectMode(%v) ok = %v, want %v", test.args, ok, test.wantOk)
			}
			if ok && mode.name != test.wantMode {
				t.Errorf("detectMode(%v) mode = %q, want %q", test.args, mode.name, test.wantMode)
			}
		})
	}
}

func TestHasHelpFlag(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{[]string{"--help"}, true},
		{[]string{"-h"}, true},
		{[]string{"--config-url", "x", "--help"}, true},
		{[]string{"--diagnostic"}, false},
		{[]string{}, false},
	}

	for _, test := range tests {
		if got := hasHelpFlag(test.args); got != test.want {
			t.Errorf("hasHelpFlag(%v) = %v, want %v", test.args, got, test.want)
		}
	}
}

func TestReportUsageHelp(t *testing.T) {
	for _, flag := range []string{"--help", "-h"} {
		t.Run(flag, func(t *testing.T) {
			args := []string{flag}
			var stdout, stderr bytes.Buffer

			code := reportUsage(args, buildModeErrs(args), &stdout, &stderr)

			if code != 0 {
				t.Errorf("expected exit code 0 for %s, got %d", flag, code)
			}

			out := stdout.String()
			if !strings.Contains(out, "Usage:") {
				t.Errorf("expected usage summary in help output, got:\n%s", out)
			}

			// The full help must include per-flag descriptions for every mode.
			for _, want := range []string{
				"Diagnostic mode:",
				"Uninstall mode:",
				"Config mode:",
				"Service mode:",
				"Update mode:",
				"--config-url",
				"--config-file",
				"--mqtt-qos",
				"--logging-level",
				"Organization ID",
			} {
				if !strings.Contains(out, want) {
					t.Errorf("expected help output to contain %q, got:\n%s", want, out)
				}
			}

			// Flag headers must use the double-dash form to match how flags are
			// invoked; no header should start with a single dash.
			singleDashHeader := regexp.MustCompile(`(?m)^  -[a-z]`)
			if singleDashHeader.MatchString(out) {
				t.Errorf("expected flag headers to use --, found single-dash header in:\n%s", out)
			}

			if stderr.Len() != 0 {
				t.Errorf("expected nothing on stderr for help, got:\n%s", stderr.String())
			}
		})
	}
}

func TestReportUsageNoArgs(t *testing.T) {
	args := []string{}
	var stdout, stderr bytes.Buffer

	code := reportUsage(args, buildModeErrs(args), &stdout, &stderr)

	if code != 1 {
		t.Errorf("expected exit code 1 for no args, got %d", code)
	}

	errOut := stderr.String()
	if !strings.Contains(errOut, "Usage:") {
		t.Errorf("expected full usage on stderr, got:\n%s", errOut)
	}
	if !strings.Contains(errOut, "--help") {
		t.Errorf("expected hint to run --help on stderr, got:\n%s", errOut)
	}
	if stdout.Len() != 0 {
		t.Errorf("expected nothing on stdout for no-args path, got:\n%s", stdout.String())
	}
}

func TestReportUsageModeErrors(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		mode    string
		wantErr string
	}{
		{
			name:    "config missing secret",
			args:    []string{"--org-id", "x", "--config-url", "https://x"},
			mode:    "config",
			wantErr: "missing config-secret",
		},
		{
			name: "config invalid mqtt-qos",
			args: []string{
				"--org-id",
				"x",
				"--config-url",
				"https://x",
				"--config-secret",
				"s",
				"--mqtt-qos",
				"5",
			},
			mode:    "config",
			wantErr: "invalid mqtt-qos: must be 0 or 1",
		},
		{
			name: "config invalid logging-level",
			args: []string{
				"--org-id",
				"x",
				"--config-url",
				"https://x",
				"--config-secret",
				"s",
				"--logging-level",
				"bogus",
			},
			mode:    "config",
			wantErr: "invalid logging-level",
		},
		{
			name:    "service missing log-file",
			args:    []string{"--org-id", "x", "--config-file", "/etc/x"},
			mode:    "service",
			wantErr: "missing log-file",
		},
		{
			name:    "update invalid mqtt-qos",
			args:    []string{"--org-id", "x", "--update", "--mqtt-qos", "5"},
			mode:    "update",
			wantErr: "invalid mqtt-qos: must be 0 or 1",
		},
		{
			name:    "update missing org-id",
			args:    []string{"--update"},
			mode:    "update",
			wantErr: "missing org-id",
		},
		{
			name:    "uninstall missing org-id",
			args:    []string{"--uninstall"},
			mode:    "uninstall",
			wantErr: "missing org-id",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer

			code := reportUsage(test.args, buildModeErrs(test.args), &stdout, &stderr)

			if code != 1 {
				t.Errorf("expected exit code 1, got %d", code)
			}

			errOut := stderr.String()
			if !strings.Contains(errOut, test.wantErr) {
				t.Errorf("expected error %q on stderr, got:\n%s", test.wantErr, errOut)
			}

			// The relevant mode's usage block must follow the error.
			wantUsage := fmt.Sprintf("Usage (%s mode):", test.mode)
			if !strings.Contains(errOut, wantUsage) {
				t.Errorf("expected %q on stderr, got:\n%s", wantUsage, errOut)
			}

			if stdout.Len() != 0 {
				t.Errorf("expected nothing on stdout for error path, got:\n%s", stdout.String())
			}
		})
	}
}
