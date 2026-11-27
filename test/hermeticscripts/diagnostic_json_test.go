package hermeticscripts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hermetic-assert-diagnostic-json runs --diagnostic --json (sc-132994) and
// checks the exit code, each named check and the secrets. A PowerShell fake
// stands in for the agent as a native process, so stdout, stderr and the exit
// code are separated the way the real binary's are: it prints FAKE_DOC on
// stdout, its arguments on stderr, and exits FAKE_EXIT.
func TestAssertDiagnosticJSON_ChecksTheExitCodeAndEachCheck(t *testing.T) {
	script := filepath.Join(t.TempDir(), "fake-agent.ps1")
	body := `Write-Output $env:FAKE_DOC
[Console]::Error.WriteLine("args: $env:FAKE_ARGS")
exit [int]$env:FAKE_EXIT
`
	if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	fake := fakeAgentWrapper(t, script)
	healthy := `{"org_id":"org-1","ok":true,"failed":[],"checks":[` +
		`{"name":"hub.direct_8883","ok":false,"advisory":true,"detail":"MQTT TLS connection (direct)"},` +
		`{"name":"hub.proxy_443","ok":true,"advisory":true,"detail":"through http://agent:xxxxx@127.0.0.1:3128"},` +
		`{"name":"hub.transport","ok":true,"advisory":false,"detail":"The agent connects"}]}`
	failing := strings.Replace(
		healthy,
		`"ok":true,"failed":[]`,
		`"ok":false,"failed":["hub.transport"]`,
		1,
	)
	failing = strings.Replace(
		failing,
		`"name":"hub.transport","ok":true`,
		`"name":"hub.transport","ok":false`,
		1,
	)
	checks := "hub.proxy_443=true\nhub.direct_8883=false\nhub.transport=true"
	assert := func(env ...string) (int, string) {
		return run(t, "hermetic-assert-diagnostic-json.ps1",
			append([]string{"AGENT_BINARY=" + fake, "ORG_ID=org-1"}, env...)...)
	}

	code, out := assert("FAKE_DOC="+healthy, "FAKE_EXIT=0", "EXPECT_EXIT=0",
		"CHECKS="+checks, "ABSENT=s3cret")
	if code != 0 || !strings.Contains(out, "args: --org-id org-1 --diagnostic --json") {
		t.Fatalf("a healthy document failed: exit %d\n%s", code, out)
	}
	code, out = assert("FAKE_DOC="+failing, "FAKE_EXIT=2", "EXPECT_EXIT=2",
		"CHECKS=hub.transport=false\nhub.proxy_443=true")
	if code != 0 {
		t.Fatalf("an expected failure failed the assertion: exit %d\n%s", code, out)
	}

	for _, c := range []struct {
		name string
		env  []string
		says string
	}{
		{"wrong exit code", []string{
			"FAKE_DOC=" + healthy, "FAKE_EXIT=2", "EXPECT_EXIT=0", "CHECKS=" + checks,
		}, "exited 2, expected 0"},
		{"a check that disagrees", []string{
			"FAKE_DOC=" + healthy, "FAKE_EXIT=0", "EXPECT_EXIT=0", "CHECKS=hub.direct_8883=true",
		}, "hub.direct_8883 is ok=false, expected true"},
		{"a missing check", []string{
			"FAKE_DOC=" + healthy, "FAKE_EXIT=0", "EXPECT_EXIT=0", "CHECKS=engine.reachable=true",
		}, "engine.reachable appears 0 time(s)"},
		{"a verdict that disagrees with the exit code", []string{
			"FAKE_DOC=" + failing, "FAKE_EXIT=0", "EXPECT_EXIT=0", "CHECKS=hub.proxy_443=true",
		}, "the document says ok=False"},
		{"a secret on stdout", []string{
			"FAKE_DOC=" + healthy, "FAKE_EXIT=0", "EXPECT_EXIT=0", "CHECKS=" + checks,
			"ABSENT=xxxxx",
		}, "must not contain: xxxxx"},
		{"not JSON", []string{
			"FAKE_DOC=[PASS] a console line", "FAKE_EXIT=0", "EXPECT_EXIT=0", "CHECKS=" + checks,
		}, "did not print one JSON document"},
		{"no checks named", []string{
			"FAKE_DOC=" + healthy, "FAKE_EXIT=0", "EXPECT_EXIT=0", "CHECKS=\n\n",
		}, "CHECKS names no check"},
		{"a malformed check", []string{
			"FAKE_DOC=" + healthy, "FAKE_EXIT=0", "EXPECT_EXIT=0", "CHECKS=hub.proxy_443",
		}, "is not name=true or name=false"},
		{"no exit code to expect", []string{
			"FAKE_DOC=" + healthy, "FAKE_EXIT=0", "CHECKS=" + checks,
		}, "EXPECT_EXIT must be the exit code"},
	} {
		code, out := assert(c.env...)
		if code == 0 || !strings.Contains(out, c.says) {
			t.Errorf("%s: exit %d, want a failure saying %q\n%s", c.name, code, c.says, out)
		}
	}
}
