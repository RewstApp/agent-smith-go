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
			input:    "no bracket here",
			expected: "no bracket here",
		},
		{
			name:     "bracket is last character",
			input:    "2024-01-01 [ERROR]",
			expected: "2024-01-01 [ERROR]",
		},
		{
			name:     "bracket followed by exactly one character",
			input:    "[INFO]x",
			expected: "",
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

func TestLevelForLine(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected logLevel
	}{
		// hclog writes "[WARN] ", never "[WARNING]", so this spelling is the
		// one that actually reaches the writer in production.
		{
			name:     "hclog warn bracket",
			input:    "2024-01-01T00:00:00.000Z [WARN]  agent_smith: disk space low",
			expected: levelWarning,
		},
		{
			name:     "spelled-out warning bracket",
			input:    "[WARNING] disk space low",
			expected: levelWarning,
		},
		{
			name:     "error bracket",
			input:    "2024-01-01T00:00:00.000Z [ERROR] agent_smith: it broke",
			expected: levelError,
		},
		{
			name:     "info bracket",
			input:    "[INFO] hello",
			expected: levelInfo,
		},
		{
			name:     "debug bracket falls back to info",
			input:    "[DEBUG] hello",
			expected: levelInfo,
		},
		{
			name:     "no bracket falls back to info",
			input:    "plain line",
			expected: levelInfo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := levelForLine(tt.input); got != tt.expected {
				t.Errorf("levelForLine(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}
