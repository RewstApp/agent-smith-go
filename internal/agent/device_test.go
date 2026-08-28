package agent

import (
	"testing"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/utils"
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
		name    string
		value   *int
		expectD time.Duration
	}{
		{"unset falls back to default", nil, DefaultCommandTimeout},
		{"zero falls back to default", intPtr(0), DefaultCommandTimeout},
		{"negative falls back to default", intPtr(-30), DefaultCommandTimeout},
		{"positive override honored", intPtr(45), 45 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Device{CommandTimeoutSeconds: tt.value}
			got := d.ResolvedCommandTimeout()
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

func TestResolvedMaxOutputBytes(t *testing.T) {
	tests := []struct {
		name   string
		value  *int
		expect int
	}{
		{"unset falls back to default", nil, DefaultMaxOutputBytes},
		{"zero falls back to default", intPtr(0), DefaultMaxOutputBytes},
		{"negative falls back to default", intPtr(-1024), DefaultMaxOutputBytes},
		{"positive override honored", intPtr(1024), 1024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Device{MaxOutputBytes: tt.value}
			if got := d.ResolvedMaxOutputBytes(); got != tt.expect {
				t.Errorf("ResolvedMaxOutputBytes() = %d, want %d", got, tt.expect)
			}
		})
	}
}

func TestSasTokenLifetime(t *testing.T) {
	tests := []struct {
		name   string
		value  *int
		expect time.Duration
	}{
		{"unset falls back to default", nil, utils.DefaultSasTokenLifetime},
		{"zero falls back to default", intPtr(0), utils.DefaultSasTokenLifetime},
		{"negative falls back to default", intPtr(-6), utils.DefaultSasTokenLifetime},
		{"positive override honored", intPtr(12), 12 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Device{SasTokenLifetimeHours: tt.value}
			if got := d.SasTokenLifetime(); got != tt.expect {
				t.Errorf("SasTokenLifetime() = %v, want %v", got, tt.expect)
			}
		})
	}
}

// TestSasTokenLifetimeOverride verifies the ldflags-injected override (used only
// by the integration test binary) forces a short lifetime and takes precedence
// over the config-driven sas_token_lifetime_hours, while an empty or invalid
// override leaves normal resolution intact.
func TestSasTokenLifetimeOverride(t *testing.T) {
	orig := sasTokenLifetimeOverrideStr
	t.Cleanup(func() { sasTokenLifetimeOverrideStr = orig })

	// A valid override wins even over an explicit per-device lifetime.
	sasTokenLifetimeOverrideStr = "90s"
	d := Device{SasTokenLifetimeHours: intPtr(12)}
	if got := d.SasTokenLifetime(); got != 90*time.Second {
		t.Errorf("with override set, SasTokenLifetime() = %v, want %v", got, 90*time.Second)
	}

	// An invalid or non-positive override is ignored, falling back to normal
	// resolution (here the per-device value).
	for _, bad := range []string{"not-a-duration", "0s", "-5s"} {
		sasTokenLifetimeOverrideStr = bad
		if got := d.SasTokenLifetime(); got != 12*time.Hour {
			t.Errorf(
				"with invalid override %q, SasTokenLifetime() = %v, want %v",
				bad,
				got,
				12*time.Hour,
			)
		}
	}

	// An empty override (production build) uses normal resolution.
	sasTokenLifetimeOverrideStr = ""
	if got := d.SasTokenLifetime(); got != 12*time.Hour {
		t.Errorf("with empty override, SasTokenLifetime() = %v, want %v", got, 12*time.Hour)
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

func TestMqttSubscribeTimeout(t *testing.T) {
	tests := []struct {
		name   string
		value  *int
		expect time.Duration
	}{
		{"unset falls back to default", nil, utils.DefaultMqttSubscribeTimeout},
		{"zero falls back to default", intPtr(0), utils.DefaultMqttSubscribeTimeout},
		{"negative falls back to default", intPtr(-5), utils.DefaultMqttSubscribeTimeout},
		{"positive override honored", intPtr(45), 45 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Device{MqttSubscribeTimeoutSeconds: tt.value}
			if got := d.MqttSubscribeTimeout(); got != tt.expect {
				t.Errorf("MqttSubscribeTimeout() = %v, want %v", got, tt.expect)
			}
		})
	}
}

func TestMqttConnectTimeout(t *testing.T) {
	tests := []struct {
		name   string
		value  *int
		expect time.Duration
	}{
		{"unset falls back to default", nil, utils.DefaultMqttConnectTimeout},
		{"zero falls back to default", intPtr(0), utils.DefaultMqttConnectTimeout},
		{"negative falls back to default", intPtr(-5), utils.DefaultMqttConnectTimeout},
		{"positive override honored", intPtr(45), 45 * time.Second},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := Device{MqttConnectTimeoutSeconds: tt.value}
			if got := d.MqttConnectTimeout(); got != tt.expect {
				t.Errorf("MqttConnectTimeout() = %v, want %v", got, tt.expect)
			}
		})
	}
}
