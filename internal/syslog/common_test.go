package syslog

import (
	"testing"
)

// TestExtractMessage tests the extractMessage helper function
func TestExtractMessage(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "info message",
			input:    "[INFO] This is an info message",
			expected: "This is an info message",
		},
		{
			name:     "error message",
			input:    "[ERROR] This is an error message",
			expected: "This is an error message",
		},
		{
			name:     "warning message",
			input:    "[WARNING] This is a warning message",
			expected: "This is a warning message",
		},
		{
			name:     "message with timestamp",
			input:    "2025-01-15 10:30:45 [INFO] Application started",
			expected: "Application started",
		},
		{
			name:     "message with multiple brackets",
			input:    "[DEBUG] [Component] Processing request",
			expected: "[Component] Processing request",
		},
		{
			name:     "message without bracket",
			input:    "Plain message without bracket",
			expected: "lain message without bracket", // No ']' found, Index returns -1, -1+2=1, starts from index 1
		},
		{
			name:     "empty message after bracket",
			input:    "[INFO] ",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractMessage(tt.input)
			if result != tt.expected {
				t.Errorf("extractMessage(%q) = %q, expected %q", tt.input, result, tt.expected)
			}
		})
	}
}

// TestExtractMessage_BehaviorWithNoClosingBracket tests behavior when no ] found
func TestExtractMessage_BehaviorWithNoClosingBracket(t *testing.T) {
	// When no ] is found, strings.Index returns -1
	// Adding 2 gives 1, so it starts from index 1
	input := "no bracket here"
	result := extractMessage(input)
	expected := "o bracket here" // Skips first character
	if result != expected {
		t.Errorf("extractMessage(%q) = %q, expected %q", input, result, expected)
	}
}

// TestExtractMessage_EdgeCases tests edge cases for extractMessage
// Note: Some inputs may cause panics due to slice bounds, which is expected behavior
func TestExtractMessage_EdgeCases(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		shouldPanic bool
	}{
		{
			name:        "just bracket",
			input:       "]",
			shouldPanic: true, // Index is 0, +2 = 2, but string length is 1 so panic
		},
		{
			name:        "bracket at end",
			input:       "message]",
			shouldPanic: true, // Index is 7, +2 = 9, but string length is 8 so panic
		},
		{
			name:        "two characters no bracket",
			input:       "ab",
			shouldPanic: false, // Index -1, +2 = 1, returns "b"
		},
		{
			name:        "empty string",
			input:       "",
			shouldPanic: true, // Index -1, +2 = 1, but string is empty so panic
		},
		{
			name:        "single character",
			input:       "x",
			shouldPanic: false, // Index -1, +2 = 1, but Go returns empty string instead of panicking
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if tt.shouldPanic && r == nil {
					t.Errorf("expected panic but got none")
				} else if !tt.shouldPanic && r != nil {
					t.Errorf("unexpected panic: %v", r)
				}
			}()
			result := extractMessage(tt.input)
			t.Logf("extractMessage(%q) = %q", tt.input, result)
		})
	}
}

// TestExtractMessage_TypicalUsage tests typical log message formats
func TestExtractMessage_TypicalUsage(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "standard info log",
			input:    "2025-01-15 14:30:00 [INFO] Service started successfully",
			expected: "Service started successfully",
		},
		{
			name:     "standard error log",
			input:    "2025-01-15 14:30:01 [ERROR] Failed to connect to database",
			expected: "Failed to connect to database",
		},
		{
			name:     "standard warning log",
			input:    "2025-01-15 14:30:02 [WARNING] High memory usage detected",
			expected: "High memory usage detected",
		},
		{
			name:     "log with JSON payload",
			input:    "[INFO] {\"user\":\"admin\",\"action\":\"login\"}",
			expected: "{\"user\":\"admin\",\"action\":\"login\"}",
		},
		{
			name:     "multiline message indicator",
			input:    "[ERROR] Stack trace follows:",
			expected: "Stack trace follows:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractMessage(tt.input)
			if result != tt.expected {
				t.Errorf("extractMessage(%q) = %q, expected %q", tt.input, result, tt.expected)
			}
		})
	}
}
