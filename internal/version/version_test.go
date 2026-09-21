package version

import "testing"

// Every expectation here is a literal. The bug this guards against survived
// because the only test of the reported version derived its expectation from
// the same Version[1:] expression as the code under test, which passes for
// "1.5.4" and ".0.0" alike.
func TestNumberOf_PinsEveryDocumentedCase(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"release tag", "v1.5.7", "1.5.7"},
		{"already bare", "1.5.7", "1.5.7"},
		{"default placeholder must not lose its first digit", "0.0.0", "0.0.0"},
		{"empty injection falls back, never panics", "", "0.0.0"},
		{"lone prefix falls back", "v", "0.0.0"},
		{"uppercase prefix", "V1.5.7", "1.5.7"},
		{"surrounding whitespace dropped", "  v1.5.7\n", "1.5.7"},
		{"prerelease suffix kept", "v1.5.7-rc1", "1.5.7-rc1"},
		{"malformed passed through unchanged", "garbage", "garbage"},
		{"only the first v is a prefix", "vv1", "v1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := numberOf(tc.in); got != tc.want {
				t.Fatalf("numberOf(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNumber_ReadsThePackageVariable(t *testing.T) {
	restore := Version
	t.Cleanup(func() { Version = restore })

	Version = "v9.8.7"
	if got := Number(); got != "9.8.7" {
		t.Fatalf("Number() = %q, want %q", got, "9.8.7")
	}
	Version = ""
	if got := Number(); got != DefaultNumber {
		t.Fatalf("Number() with empty Version = %q, want %q", got, DefaultNumber)
	}
}

// The default must already be in release-tag shape so a go-build binary and a
// release binary differ only in the number. If someone resets it to "0.0.0",
// Number still reports it correctly, but this test says why the shape matters.
func TestDefaultVersion_HasReleaseTagShape(t *testing.T) {
	if Version != "v0.0.0" {
		t.Fatalf(
			"default Version = %q, want %q (same shape the build script injects)",
			Version,
			"v0.0.0",
		)
	}
	if got := Number(); got != "0.0.0" {
		t.Fatalf("Number() of the default = %q, want %q", got, "0.0.0")
	}
}
