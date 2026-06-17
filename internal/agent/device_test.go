package agent

import "testing"

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
