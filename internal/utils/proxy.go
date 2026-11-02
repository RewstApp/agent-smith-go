package utils

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
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
//     environment, with Go's standard rules (which never proxy loopback).
//
// Credentials in a proxy URL are never logged: use RedactURL.

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
// the environment when explicit is empty. An invalid explicit URL is an error
// rather than a silent direct connection.
func ResolveProxy(explicit string) (ProxyFunc, error) {
	if strings.TrimSpace(explicit) == "" {
		return http.ProxyFromEnvironment, nil
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

// RedactURL renders raw with any password replaced, for logs and the
// diagnostic. An unparseable value is withheld entirely, since it may still
// hold a credential.
func RedactURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return "[unparseable proxy URL withheld]"
	}
	return u.Redacted()
}

// DescribeProxy renders the proxy for logs: the redacted URL, or "none".
func DescribeProxy(u *url.URL) string {
	if u == nil {
		return "none"
	}
	return u.Redacted()
}

func noProxyEnv() string {
	if v := os.Getenv("NO_PROXY"); v != "" {
		return v
	}
	return os.Getenv("no_proxy")
}

// noProxyList is a parsed NO_PROXY value: "*" (everything), host names (which
// match the name and its subdomains, with or without a leading dot), IP
// addresses and CIDR ranges, each optionally with a port.
type noProxyList struct {
	all     bool
	entries []noProxyEntry
}

type noProxyEntry struct {
	domain string
	ip     net.IP
	cidr   *net.IPNet
	port   string
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
		list.entries = append(
			list.entries,
			noProxyEntry{domain: strings.TrimPrefix(host, "."), port: port},
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
			if host == e.domain || strings.HasSuffix(host, "."+e.domain) {
				return true
			}
		}
	}
	return false
}
