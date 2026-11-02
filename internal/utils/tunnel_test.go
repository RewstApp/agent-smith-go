package utils

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// connectProxy is a minimal CONNECT proxy that records the Proxy-Authorization
// it saw and answers with status (raw status line) before tunnelling to target.
type connectProxy struct {
	ln       net.Listener
	status   string
	target   string
	mu       sync.Mutex
	sawAuth  []string
	sawHosts []string
}

func newConnectProxy(t *testing.T, status, target string) *connectProxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &connectProxy{ln: ln, status: status, target: target}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go p.handle(c)
		}
	}()
	return p
}

func (p *connectProxy) handle(c net.Conn) {
	defer func() { _ = c.Close() }()
	req, err := http.ReadRequest(bufio.NewReader(c))
	if err != nil {
		return
	}
	p.mu.Lock()
	p.sawAuth = append(p.sawAuth, req.Header.Get("Proxy-Authorization"))
	p.sawHosts = append(p.sawHosts, req.Host)
	p.mu.Unlock()
	_, _ = io.WriteString(c, p.status+"\r\nContent-Length: 0\r\n\r\n")
	if !strings.Contains(p.status, " 200") {
		return
	}
	upstream, err := net.Dial("tcp", p.target)
	if err != nil {
		return
	}
	defer func() { _ = upstream.Close() }()
	go func() { _, _ = io.Copy(upstream, c) }()
	_, _ = io.Copy(c, upstream)
}

func (p *connectProxy) url(user *url.Userinfo) *url.URL {
	return &url.URL{Scheme: "http", Host: p.ln.Addr().String(), User: user}
}

func TestDialTunnel_ConnectsThroughAnAuthenticatedProxy(t *testing.T) {
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "through the tunnel")
	}))
	defer echo.Close()
	target := echo.Listener.Addr().String()
	p := newConnectProxy(t, "HTTP/1.1 200 Connection Established", target)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := DialTunnel(ctx, p.url(url.UserPassword("agent", "s3cret")), "hub.example:443")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_, _ = io.WriteString(conn, "GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
	body, _ := io.ReadAll(conn)
	if !strings.Contains(string(body), "through the tunnel") {
		t.Errorf("tunnel carried %q", body)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("agent:s3cret"))
	if p.sawAuth[0] != want || p.sawHosts[0] != "hub.example:443" {
		t.Errorf("proxy saw auth %q host %q", p.sawAuth[0], p.sawHosts[0])
	}
}

// A user with no password is still sent, as "user:" - the same credentials
// the diagnostic and the agent present.
func TestDialTunnel_SendsAUserWithoutPassword(t *testing.T) {
	p := newConnectProxy(t, "HTTP/1.1 407 Proxy Authentication Required", "")
	_, err := DialTunnel(context.Background(), p.url(url.User("TOKEN")), "hub.example:443")
	if err == nil {
		t.Fatal("a 407 was taken as a tunnel")
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte("TOKEN:"))
	if p.sawAuth[0] != want {
		t.Errorf("auth %q, want %q", p.sawAuth[0], want)
	}
	if strings.Contains(err.Error(), "TOKEN") {
		t.Errorf("the error leaks the credential: %v", err)
	}
}

// A status line with no reason phrase - legal in HTTP/1.1 - is an error,
// never a panic (gorilla's own proxy dialer indexes past it).
func TestDialTunnel_BareStatusLineIsAnErrorNotAPanic(t *testing.T) {
	p := newConnectProxy(t, "HTTP/1.1 407", "")
	_, err := DialTunnel(context.Background(), p.url(nil), "hub.example:443")
	if err == nil || !strings.Contains(err.Error(), "407") {
		t.Errorf("err %v", err)
	}
}

func TestDialTunnel_DirectWithoutAProxy(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	conn, err := DialTunnel(context.Background(), nil, ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
}

// An https:// proxy is reached over TLS before the CONNECT.
func TestDialTunnel_HTTPSProxy(t *testing.T) {
	var sawConnect bool
	var mu sync.Mutex
	proxy := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sawConnect = r.Method == http.MethodConnect && r.Host == "hub.example:443"
		mu.Unlock()
		w.WriteHeader(http.StatusForbidden)
	}))
	defer proxy.Close()
	u, _ := url.Parse(proxy.URL)
	_, err := DialTunnel(context.Background(), u, "hub.example:443")
	// The test server's certificate is not trusted, so the TLS step to the
	// proxy fails - which proves the proxy was dialled over TLS.
	if err == nil || !strings.Contains(err.Error(), "TLS to the proxy") {
		t.Errorf("err %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if sawConnect {
		t.Error("CONNECT was sent before TLS to the proxy succeeded")
	}
}
