package utils

import (
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
	clearProxyEnv(t)
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

// A blank field defers to the environment, read when the proxy is resolved -
// not cached for the process the way http.ProxyFromEnvironment does.
func TestResolveProxy_EnvironmentWhenUnset(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("HTTPS_PROXY", "http://env-proxy:8080")
	t.Setenv("NO_PROXY", "skip.example.com")
	fn, err := ResolveProxy("  ")
	if err != nil {
		t.Fatal(err)
	}
	for target, want := range map[string]string{
		"https://hub.example.net/":  "http://env-proxy:8080",
		"https://skip.example.com/": "none",
		// Go's environment rules never proxy loopback.
		"https://127.0.0.1:8443/": "none",
	} {
		if got := proxyOf(t, fn, target); got != want {
			t.Errorf("%s: %s, want %s", target, got, want)
		}
	}
	clearProxyEnv(t)
	fn, _ = ResolveProxy("")
	if got := proxyOf(t, fn, "https://hub.example.net/"); got != "none" {
		t.Errorf("the environment was cached: %s", got)
	}
}

func clearProxyEnv(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "NO_PROXY", "no_proxy",
		"REQUEST_METHOD",
	} {
		t.Setenv(name, "")
	}
}

// NO_PROXY follows Go's semantics: a bare name matches itself and its
// subdomains, ".name" and "*.name" subdomains only.
func TestResolveProxy_NoProxyFollowsGoSemantics(t *testing.T) {
	clearProxyEnv(t)
	t.Setenv("NO_PROXY", "*.corp.local,.dot.example,bare.example")
	fn, _ := ResolveProxy("http://proxy:3128")
	for target, want := range map[string]string{
		"https://rewst.corp.local/": "none",
		"https://corp.local/":       "http://proxy:3128",
		"https://a.dot.example/":    "none",
		"https://dot.example/":      "http://proxy:3128",
		"https://bare.example/":     "none",
		"https://sub.bare.example/": "none",
		"https://notbare.example/":  "http://proxy:3128",
	} {
		if got := proxyOf(t, fn, target); got != want {
			t.Errorf("%s: %s, want %s", target, got, want)
		}
	}
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "lower.example")
	fn, _ = ResolveProxy("http://proxy:3128")
	if got := proxyOf(t, fn, "https://lower.example/"); got != "none" {
		t.Errorf("lowercase no_proxy ignored: %s", got)
	}
}

func TestResolveProxy_NoProxyStarExcludesEverything(t *testing.T) {
	clearProxyEnv(t)
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
		"http://user:@proxy:3128":       "http://user:xxxxx@proxy:3128",
		"http://TOKEN123@proxy:3128":    "http://xxxxx@proxy:3128",
		"http://proxy:3128":             "http://proxy:3128",
		"":                              "",
		"   ":                           "",
	} {
		if got := RedactURL(raw); got != want {
			t.Errorf("RedactURL(%q) = %q, want %q", raw, got, want)
		}
	}
	if got := RedactURL("http://user:s3cret@[bad"); strings.Contains(got, "s3cret") {
		t.Errorf("an unparseable URL leaked its password: %q", got)
	}
}
