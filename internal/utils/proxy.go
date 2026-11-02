package utils

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/net/http/httpproxy"
)

// Outbound proxy support (sc-129691). Every connection the agent makes - the
// MQTT WebSocket transport, the postback client, the config fetch and the
// auto-update release and download clients - resolves its proxy the same way:
//
//  1. an explicit proxy URL (the proxy_url config field or --proxy-url) is used
//     for every destination except those NO_PROXY names, loopback included,
//     because a service rarely inherits a user's proxy environment and an
//     operator who sets one means it;
//  2. otherwise HTTPS_PROXY / HTTP_PROXY / NO_PROXY from the process
//     environment, with Go's standard rules (which never proxy loopback),
//     read when the proxy is resolved rather than cached for the process.
//
// A blank value is unset. Credentials in a proxy URL are never logged: use
// RedactURL or DescribeProxy.

// ProxyFunc is the signature net/http and the WebSocket dialer take.
type ProxyFunc = func(*http.Request) (*url.URL, error)

// ParseProxyURL validates an explicit proxy URL: http or https, with a host.
func ParseProxyURL(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("invalid proxy-url: %s", RedactURL(raw))
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("invalid proxy-url: scheme must be http or https, got %q", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("invalid proxy-url: no host in %s", RedactURL(raw))
	}
	return u, nil
}

// ResolveProxy returns the proxy function for an explicit proxy URL, or for
// the environment when explicit is blank. An invalid explicit URL is an error
// rather than a silent direct connection.
func ResolveProxy(explicit string) (ProxyFunc, error) {
	if strings.TrimSpace(explicit) == "" {
		fromEnv := httpproxy.FromEnvironment().ProxyFunc()
		return func(req *http.Request) (*url.URL, error) {
			if req == nil || req.URL == nil {
				return nil, nil
			}
			return fromEnv(req.URL)
		}, nil
	}
	proxy, err := ParseProxyURL(explicit)
	if err != nil {
		return nil, err
	}
	noProxy := parseNoProxy(noProxyEnv())
	return func(req *http.Request) (*url.URL, error) {
		if req == nil || req.URL == nil || noProxy.matches(req.URL) {
			return nil, nil
		}
		return proxy, nil
	}, nil
}

// ProxyFor reports the proxy fn would use for target, nil for a direct
// connection.
func ProxyFor(fn ProxyFunc, target *url.URL) *url.URL {
	if fn == nil || target == nil {
		return nil
	}
	u, err := fn(&http.Request{URL: target, Header: http.Header{}})
	if err != nil {
		return nil
	}
	return u
}

// ProxyTransport is http.DefaultTransport's configuration with fn as its proxy.
func ProxyTransport(fn ProxyFunc) *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = fn
	return t
}

// RedactURL renders raw with its credentials masked, for logs and the
// diagnostic. An unparseable value is withheld entirely, since it may still
// hold a credential.
func RedactURL(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "[unparseable proxy URL withheld]"
	}
	return DescribeProxy(u)
}

// DescribeProxy renders a proxy for logs: "none", or the URL with its
// credentials masked. A password is masked and the user name kept; a user name
// with no password is masked too, since a proxy that takes a token as the user
// name carries the credential there.
func DescribeProxy(u *url.URL) string {
	if u == nil {
		return "none"
	}
	if u.User == nil {
		return u.String()
	}
	masked := *u
	if _, hasPassword := u.User.Password(); hasPassword {
		masked.User = url.UserPassword(u.User.Username(), "xxxxx")
	} else {
		masked.User = url.User("xxxxx")
	}
	return masked.String()
}

func noProxyEnv() string {
	if v := os.Getenv("NO_PROXY"); v != "" {
		return v
	}
	return os.Getenv("no_proxy")
}

// noProxyList is a parsed NO_PROXY value with Go's semantics
// (golang.org/x/net/http/httpproxy): "*" matches everything; "example.com"
// matches the name and its subdomains; ".example.com" and "*.example.com"
// match subdomains only; IP addresses and CIDR ranges match addresses; any
// entry may carry a port. Unlike httpproxy, loopback is not exempt: an
// explicit proxy applies to it unless NO_PROXY names it.
type noProxyList struct {
	all     bool
	entries []noProxyEntry
}

type noProxyEntry struct {
	// suffix is ".example.com"; matchBare also matches "example.com" itself.
	suffix    string
	matchBare bool
	ip        net.IP
	cidr      *net.IPNet
	port      string
}

func parseNoProxy(value string) noProxyList {
	var list noProxyList
	for _, raw := range strings.Split(value, ",") {
		entry := strings.ToLower(strings.TrimSpace(raw))
		if entry == "" {
			continue
		}
		if entry == "*" {
			list.all = true
			continue
		}
		if _, cidr, err := net.ParseCIDR(entry); err == nil {
			list.entries = append(list.entries, noProxyEntry{cidr: cidr})
			continue
		}
		host, port := entry, ""
		if h, p, err := net.SplitHostPort(entry); err == nil {
			host, port = h, p
		}
		host = strings.Trim(host, "[]")
		if ip := net.ParseIP(host); ip != nil {
			list.entries = append(list.entries, noProxyEntry{ip: ip, port: port})
			continue
		}
		host = strings.TrimPrefix(host, "*")
		if host == "" || host == "." {
			continue
		}
		matchBare := !strings.HasPrefix(host, ".")
		if matchBare {
			host = "." + host
		}
		list.entries = append(
			list.entries,
			noProxyEntry{suffix: host, matchBare: matchBare, port: port},
		)
	}
	return list
}

func (l noProxyList) matches(target *url.URL) bool {
	if l.all {
		return true
	}
	host := strings.ToLower(target.Hostname())
	port := target.Port()
	if port == "" {
		switch target.Scheme {
		case "https", "wss":
			port = "443"
		case "http", "ws":
			port = "80"
		}
	}
	ip := net.ParseIP(host)
	for _, e := range l.entries {
		if e.port != "" && e.port != port {
			continue
		}
		switch {
		case e.cidr != nil:
			if ip != nil && e.cidr.Contains(ip) {
				return true
			}
		case e.ip != nil:
			if ip != nil && e.ip.Equal(ip) {
				return true
			}
		default:
			if strings.HasSuffix(host, e.suffix) || (e.matchBare && host == e.suffix[1:]) {
				return true
			}
		}
	}
	return false
}
