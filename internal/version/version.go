// Package version holds the build-time version stamp and the one helper that
// turns it into the bare number the agent reports over the wire.
package version

import "strings"

// Version is the agent's release tag as stamped by the build script
// (scripts/build.ps1: -X ...version.Version=v$(cz version -p)), e.g. "v1.5.7".
// The default deliberately has the same shape, so a binary from a plain
// `go build`, `go run` or `go test` differs from a release binary only in the
// number, never in the format — a bare "0.0.0" default is what made the old
// Version[1:] slice report ".0.0" from every developer and CI build.
//
// This is the form logged at startup, shown by --diagnostic, written to the
// device twin's agent_version and compared against GitHub release tags by the
// auto-updater, all of which want the tag itself. Anything that has always
// carried the bare number — the x-rewst-agent-smith-version header and the
// AGENT_SMITH_VERSION variable exported into every command script — goes
// through Number instead.
var Version = "v0.0.0"

// DefaultNumber is what Number returns when Version carries nothing after the
// prefix (an empty -X injection from a misconfigured CI step). A build mistake
// then reports a recognisably placeholder version instead of panicking on the
// first outbound HTTP request, which is the config fetch during install.
const DefaultNumber = "0.0.0"

// Number returns Version as the bare number the wire formats expect. It is
// total — every input has a defined, non-panicking result:
//
//	"v1.5.7"    -> "1.5.7"   (the leading v is stripped)
//	"1.5.7"     -> "1.5.7"   (a v that is not there is not stripped from)
//	"0.0.0"     -> "0.0.0"   (the first digit is never mistaken for a prefix)
//	""          -> "0.0.0"   (DefaultNumber, never an index-out-of-range panic)
//	"v"         -> "0.0.0"   (DefaultNumber)
//	"  v1.5.7\n" -> "1.5.7"   (surrounding whitespace is dropped)
//	"1.5.7-rc1" -> "1.5.7-rc1" (a suffix is kept; this is formatting, not validation)
//	"garbage"   -> "garbage" (passed through; the updater's semver parse is where
//	                          a malformed value is rejected, with an error naming it)
func Number() string {
	return numberOf(Version)
}

// numberOf is Number over an explicit value so the table test can pin every
// case above to a literal without mutating the package variable.
func numberOf(v string) string {
	v = strings.TrimSpace(v)
	if len(v) > 0 && (v[0] == 'v' || v[0] == 'V') {
		v = v[1:]
	}
	if v == "" {
		return DefaultNumber
	}
	return v
}
