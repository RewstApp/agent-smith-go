package mqtt

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/gorilla/websocket"
)

// The WebSocket transport goes through the agent's own CONNECT tunnel: it
// carries MQTT bytes both ways to a WebSocket endpoint behind an http://
// proxy, presents the proxy's credentials, and a refusal - even one with a
// bare status line - is an error rather than a panic (sc-129691).
func TestOpenConnection_WebSocketThroughTheProxy(t *testing.T) {
	upgrader := websocket.Upgrader{Subprotocols: []string{"mqtt"}}
	ws := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/$iothub/websocket" {
			http.NotFound(w, r)
			return
		}
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		for {
			kind, msg, err := c.ReadMessage()
			if err != nil {
				return
			}
			_ = c.WriteMessage(kind, append([]byte("echo:"), msg...))
		}
	}))
	defer ws.Close()

	var mu sync.Mutex
	var auth, host string
	status := "HTTP/1.1 200 Connection Established"
	proxyLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = proxyLn.Close() }()
	go func() {
		for {
			c, err := proxyLn.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				req, err := http.ReadRequest(bufio.NewReader(c))
				if err != nil {
					return
				}
				mu.Lock()
				auth, host = req.Header.Get("Proxy-Authorization"), req.Host
				line := status
				mu.Unlock()
				_, _ = io.WriteString(c, line+"\r\n\r\n")
				if !strings.HasSuffix(line, "Established") {
					return
				}
				up, err := net.Dial("tcp", ws.Listener.Addr().String())
				if err != nil {
					return
				}
				defer func() { _ = up.Close() }()
				go func() { _, _ = io.Copy(up, c) }()
				_, _ = io.Copy(c, up)
			}(c)
		}
	}()
	proxyURL := &url.URL{
		Scheme: "http",
		Host:   proxyLn.Addr().String(),
		User:   url.UserPassword("agent", "s3cret"),
	}
	proxy := func(*http.Request) (*url.URL, error) { return proxyURL, nil }
	broker, _ := url.Parse("wss://hub.example:443/$iothub/websocket")
	opts := mqtt.NewClientOptions()
	opts.ConnectTimeout = 5 * time.Second
	opts.TLSConfig = &tls.Config{
		RootCAs:    certPool(ws),
		ServerName: "example.com",
		MinVersion: tls.VersionTLS12,
	}

	conn, err := openConnection(proxy)(broker, *opts)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.Write([]byte("CONNECT-packet")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Read(buf)
	if err != nil || string(buf[:n]) != "echo:CONNECT-packet" {
		t.Fatalf("read %q %v", buf[:n], err)
	}
	mu.Lock()
	if host != "hub.example:443" || !strings.HasPrefix(auth, "Basic ") {
		t.Errorf("proxy saw host %q auth %q", host, auth)
	}
	status = "HTTP/1.1 407"
	mu.Unlock()

	if _, err := openConnection(proxy)(broker, *opts); err == nil ||
		!strings.Contains(err.Error(), "407") {
		t.Errorf("a bare 407 gave %v", err)
	}
}

func certPool(srv *httptest.Server) *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return pool
}
