package agent

import (
	"testing"
	"time"
)

func intPtr(v int) *int { return &v }

func TestResolvedWorkerCount(t *testing.T) {
	tests := []struct {
		name   string
		value  *int
		expect int
	}{
		{"unset falls back to default", nil, DefaultWorkerCount},
		{"zero falls back to default", intPtr(0), DefaultWorkerCount},
		{"negative falls back to default", intPtr(-5), DefaultWorkerCount},
		{"positive override honored", intPtr(25), 25},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Device{WorkerCount: tt.value}
			if got := d.ResolvedWorkerCount(); got != tt.expect {
				t.Errorf("ResolvedWorkerCount() = %d, want %d", got, tt.expect)
			}
		})
	}
}

func TestResolvedCommandTimeout(t *testing.T) {
	tests := []struct {
		name     string
		value    *int
		expectOk bool
		expectD  time.Duration
	}{
		{"unset is unbounded", nil, false, 0},
		{"zero is unbounded", intPtr(0), false, 0},
		{"negative is unbounded", intPtr(-30), false, 0},
		{"positive override honored", intPtr(45), true, 45 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Device{CommandTimeoutSeconds: tt.value}
			got, ok := d.ResolvedCommandTimeout()
			if ok != tt.expectOk {
				t.Errorf("ResolvedCommandTimeout() ok = %v, want %v", ok, tt.expectOk)
			}
			if got != tt.expectD {
				t.Errorf("ResolvedCommandTimeout() = %v, want %v", got, tt.expectD)
			}
		})
	}
}

func TestResolvedMessageQueueSize(t *testing.T) {
	tests := []struct {
		name   string
		value  *int
		expect int
	}{
		{"unset falls back to default", nil, DefaultMessageQueueSize},
		{"zero falls back to default", intPtr(0), DefaultMessageQueueSize},
		{"negative falls back to default", intPtr(-1), DefaultMessageQueueSize},
		{"positive override honored", intPtr(500), 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Device{MessageQueueSize: tt.value}
			if got := d.ResolvedMessageQueueSize(); got != tt.expect {
				t.Errorf("ResolvedMessageQueueSize() = %d, want %d", got, tt.expect)
			}
		})
	}
}

func TestResolvedPostbackMaxAttempts(t *testing.T) {
	tests := []struct {
		name   string
		value  *int
		expect int
	}{
		{"unset falls back to default", nil, DefaultPostbackMaxAttempts},
		{"zero falls back to default", intPtr(0), DefaultPostbackMaxAttempts},
		{"negative falls back to default", intPtr(-3), DefaultPostbackMaxAttempts},
		{"positive override honored", intPtr(10), 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Device{PostbackMaxAttempts: tt.value}
			if got := d.ResolvedPostbackMaxAttempts(); got != tt.expect {
				t.Errorf("ResolvedPostbackMaxAttempts() = %d, want %d", got, tt.expect)
			}
		})
	}
}

func TestResolvedPostbackBaseRetryBackoff(t *testing.T) {
	tests := []struct {
		name   string
		value  *int
		expect time.Duration
	}{
		{"unset falls back to default", nil, DefaultPostbackBaseRetryBackoff},
		{"zero falls back to default", intPtr(0), DefaultPostbackBaseRetryBackoff},
		{"negative falls back to default", intPtr(-2), DefaultPostbackBaseRetryBackoff},
		{"positive override honored", intPtr(5), 5 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Device{PostbackBaseRetryBackoffSeconds: tt.value}
			if got := d.ResolvedPostbackBaseRetryBackoff(); got != tt.expect {
				t.Errorf("ResolvedPostbackBaseRetryBackoff() = %v, want %v", got, tt.expect)
			}
		})
	}
}
