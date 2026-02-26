package syslog

import "testing"

func TestExtractMessage(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "info prefix",
			input:    "[INFO] some message",
			expected: "some message",
		},
		{
			name:     "debug prefix",
			input:    "[DEBUG] debug output",
			expected: "debug output",
		},
		{
			name:     "warn prefix",
			input:    "[WARN] something went wrong",
			expected: "something went wrong",
		},
		{
			name:     "error prefix",
			input:    "[ERROR] fatal error occurred",
			expected: "fatal error occurred",
		},
		{
			name:     "message with brackets in content",
			input:    "[INFO] result [ok]",
			expected: "result [ok]",
		},
		{
			name:     "no bracket in line",
			input:    "xno bracket here",
			expected: "no bracket here",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractMessage(tt.input)
			if got != tt.expected {
				t.Errorf("extractMessage(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}
