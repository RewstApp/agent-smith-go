package mqtt

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestGenerateSASToken(t *testing.T) {
	resourceURI := "my-iot-hub.azure-devices.net/devices/mydevice"
	key := "c2VjcmV0a2V5" // "secretkey" in base64

	token, err := generateSASToken(resourceURI, key, 1*time.Hour)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if !strings.HasPrefix(token, "SharedAccessSignature") {
		t.Errorf("expected token to start with 'SharedAccessSignature', got %s", token)
	}

	if !strings.Contains(token, "sr="+resourceURI) {
		t.Errorf("expected to have sr=URI, got %s", token)
	}

	if !strings.Contains(token, "sig=") {
		t.Errorf("expected to have sig=, got %s", token)
	}

	if !strings.Contains(token, "se=") {
		t.Errorf("expected to have se=, got %s", token)
	}
}

// parseSASTokenExpiry extracts the se= (expiry, unix seconds) field from a SAS
// token so tests can assert the token lifetime was honored.
func parseSASTokenExpiry(t *testing.T, token string) int64 {
	t.Helper()
	for _, part := range strings.Split(token, "&") {
		if suffix, ok := strings.CutPrefix(part, "se="); ok {
			exp, err := strconv.ParseInt(suffix, 10, 64)
			if err != nil {
				t.Fatalf("failed to parse se= from token %q: %v", token, err)
			}
			return exp
		}
	}
	t.Fatalf("token %q missing se= expiry field", token)
	return 0
}

func TestGenerateSASTokenHonorsLifetime(t *testing.T) {
	resourceURI := "my-iot-hub.azure-devices.net/devices/mydevice"
	key := "c2VjcmV0a2V5" // "secretkey" in base64

	const lifetime = 24 * time.Hour
	before := time.Now().Add(lifetime).Unix()
	token, err := generateSASToken(resourceURI, key, lifetime)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	after := time.Now().Add(lifetime).Unix()

	exp := parseSASTokenExpiry(t, token)
	if exp < before || exp > after {
		t.Errorf(
			"expected token expiry within [%d, %d] (now + %v), got %d",
			before,
			after,
			lifetime,
			exp,
		)
	}
}

func TestNewAzureIotHubClientOptionsTokenLifetime(t *testing.T) {
	base := azureIotHubDevice{
		DeviceId:        "testdevice",
		Host:            "testhub.azure-devices.net",
		SharedAccessKey: "c2VjcmV0a2V5", // "secretkey" in base64
	}

	// A zero lifetime falls back to the documented default rather than minting an
	// already-expired token.
	unsetOpts, err := newAzureIotHubClientOptions(base)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	unsetExp := parseSASTokenExpiry(t, unsetOpts.Password)
	if unsetExp <= time.Now().Unix() {
		t.Errorf("expected default-lifetime token to expire in the future, got %d", unsetExp)
	}

	// A short explicit lifetime must yield an earlier expiry than the (much
	// longer) default, proving the configured lifetime is threaded through.
	shortDevice := base
	shortDevice.TokenLifetime = time.Hour
	shortOpts, err := newAzureIotHubClientOptions(shortDevice)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	shortExp := parseSASTokenExpiry(t, shortOpts.Password)
	if shortExp >= unsetExp {
		t.Errorf(
			"expected 1h-lifetime token (exp %d) to expire before the default-lifetime token (exp %d)",
			shortExp,
			unsetExp,
		)
	}
}

func TestNewAzureIotHubClientOptions(t *testing.T) {
	device := azureIotHubDevice{
		DeviceId:        "testdevice",
		Host:            "testhub.azure-devices.net",
		SharedAccessKey: "c2VjcmV0a2V5", // "secretkey" in base64
	}

	opts, err := newAzureIotHubClientOptions(device)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if opts.ClientID != device.DeviceId {
		t.Errorf("expected ClientID to be %s, got %s", device.DeviceId, opts.ClientID)
	}

	expectedUsername := device.Host + "/" + device.DeviceId + "/?api-version=2021-04-12"
	if opts.Username != expectedUsername {
		t.Errorf("expected Username to be %s, got %s", expectedUsername, opts.Username)
	}

	if opts.Password == "" {
		t.Errorf("expected Password (SAS token) to be set")
	}

	if opts.TLSConfig == nil {
		t.Errorf("expected TLS config to be set")
	}
}
