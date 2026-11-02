package utils

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func mustURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func proxyOf(t *testing.T, fn ProxyFunc, target string) string {
	t.Helper()
	return DescribeProxy(ProxyFor(fn, mustURL(t, target)))
}

// The explicit proxy URL wins over the environment, and applies to loopback
// too; NO_PROXY still excludes what it names.
func TestResolveProxy_ExplicitOverEnvironment(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://env-proxy:8080")
	t.Setenv("HTTP_PROXY", "http://env-proxy:8080")
	t.Setenv("NO_PROXY", "internal.example.com,10.0.0.0/8,192.168.1.5,skip.example.org:8443")
	fn, err := ResolveProxy("http://user:secret@proxy.corp:3128")
	if err != nil {
		t.Fatal(err)
	}
	for target, want := range map[string]string{
		"https://hub.azure-devices.net/$iothub/websocket": "http://user:xxxxx@proxy.corp:3128",
		"https://127.0.0.1:8443/webhooks":                 "http://user:xxxxx@proxy.corp:3128",
		"https://internal.example.com/x":                  "none",
		"https://a.internal.example.com/x":                "none",
		"https://notinternal.example.com/x":               "http://user:xxxxx@proxy.corp:3128",
		"https://10.1.2.3/x":                              "none",
		"https://192.168.1.5/x":                           "none",
		"https://skip.example.org:8443/x":                 "none",
		"https://skip.example.org/x":                      "http://user:xxxxx@proxy.corp:3128",
	} {
		if got := proxyOf(t, fn, target); got != want {
			t.Errorf("%s: proxy %s, want %s", target, got, want)
		}
	}
}

func TestResolveProxy_EnvironmentWhenUnset(t *testing.T) {
	// http.ProxyFromEnvironment reads the environment once per process, so
	// only its wiring is checked here: an unset field yields it.
	fn, err := ResolveProxy("  ")
	if err != nil {
		t.Fatal(err)
	}
	req := &http.Request{URL: mustURL(t, "https://hub.example.net/")}
	if _, err := fn(req); err != nil {
		t.Errorf("environment proxy: %v", err)
	}
}

func TestResolveProxy_NoProxyStarExcludesEverything(t *testing.T) {
	t.Setenv("NO_PROXY", "*")
	fn, _ := ResolveProxy("http://proxy.corp:3128")
	if got := proxyOf(t, fn, "https://hub.example.net/"); got != "none" {
		t.Errorf("NO_PROXY=* still proxied: %s", got)
	}
}

func TestParseProxyURL_Validates(t *testing.T) {
	for _, bad := range []string{"socks5://p:1080", "proxy.corp:3128", "http://", "::::"} {
		if _, err := ParseProxyURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := ResolveProxy("ftp://user:secret@p:21"); err == nil ||
		strings.Contains(err.Error(), "secret") {
		t.Errorf("invalid scheme error: %v", err)
	}
	for _, good := range []string{"http://proxy:3128", "https://u:p@proxy.corp"} {
		if _, err := ParseProxyURL(good); err != nil {
			t.Errorf("%q rejected: %v", good, err)
		}
	}
}

func TestRedactURL_NeverShowsThePassword(t *testing.T) {
	for raw, want := range map[string]string{
		"http://user:s3cret@proxy:3128": "http://user:xxxxx@proxy:3128",
		"http://proxy:3128":             "http://proxy:3128",
		"":                              "",
	} {
		if got := RedactURL(raw); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", raw, got, want)
		}
	}
	if got := RedactURL("http://user:s3cret@[bad"); strings.Contains(got, "s3cret") {
		t.Errorf("an unparseable URL leaked its password: %q", got)
	}
}
