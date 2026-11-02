//go:build integration

package main

// Outbound-proxy fixture (sc-129691): IoT Hub's MQTT-over-WebSocket endpoint,
// an HTTP proxy that can carry the agent to it, and a switch that refuses
// direct MQTT on 8883 - the hermetic stand-in for a network whose only egress
// is a proxy. Refusing at the broker, rather than with a firewall rule, works
// the same on every platform: Windows Firewall does not filter loopback.

import (
	"bufio"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// refuseDirect, when set, makes the 8883 listener close every connection on
// accept, before the TLS handshake.
var refuseDirect atomic.Bool

// webSocketPath is where IoT Hub serves MQTT over WebSocket.
const webSocketPath = "/$iothub/websocket"

// serveWebSocket runs the broker's MQTT session over WebSocket on addr, with
// the broker's own certificate.
func serveWebSocket(addr string, tlsConfig *tls.Config, modeFile string) {
	srv := &http.Server{
		Addr:              addr,
		Handler:           webSocketHandler(modeFile),
		TLSConfig:         tlsConfig.Clone(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("stub broker WebSocket endpoint listening on %s%s", addr, webSocketPath)
	if err := srv.ListenAndServeTLS("", ""); err != nil {
		log.Fatalf("WebSocket listener failed: %v", err)
	}
}

// webSocketHandler upgrades requests at webSocketPath and runs the MQTT
// session over them.
func webSocketHandler(modeFile string) http.Handler {
	upgrader := websocket.Upgrader{
		Subprotocols: []string{"mqtt"},
		CheckOrigin:  func(*http.Request) bool { return true },
	}
	mux := http.NewServeMux()
	mux.HandleFunc(webSocketPath, func(w http.ResponseWriter, r *http.Request) {
		ws, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("[%s] WebSocket upgrade failed: %v", r.RemoteAddr, err)
			return
		}
		log.Printf("[%s] MQTT over WebSocket", r.RemoteAddr)
		serve(&wsConn{ws: ws}, modeFile)
	})
	return mux
}

// wsConn presents a WebSocket as the byte stream the MQTT session reads and
// writes: each write is one binary message, and reads run across messages.
type wsConn struct {
	ws  *websocket.Conn
	r   io.Reader
	wmu sync.Mutex
}

func (c *wsConn) Read(p []byte) (int, error) {
	for {
		if c.r == nil {
			kind, r, err := c.ws.NextReader()
			if err != nil {
				return 0, err
			}
			if kind != websocket.BinaryMessage {
				continue
			}
			c.r = r
		}
		n, err := c.r.Read(p)
		if err == io.EOF {
			c.r = nil
			if n > 0 {
				return n, nil
			}
			continue
		}
		return n, err
	}
}

func (c *wsConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := c.ws.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *wsConn) Close() error         { return c.ws.Close() }
func (c *wsConn) LocalAddr() net.Addr  { return c.ws.LocalAddr() }
func (c *wsConn) RemoteAddr() net.Addr { return c.ws.RemoteAddr() }
func (c *wsConn) SetDeadline(t time.Time) error {
	if err := c.ws.SetReadDeadline(t); err != nil {
		return err
	}
	return c.ws.SetWriteDeadline(t)
}
func (c *wsConn) SetReadDeadline(t time.Time) error  { return c.ws.SetReadDeadline(t) }
func (c *wsConn) SetWriteDeadline(t time.Time) error { return c.ws.SetWriteDeadline(t) }

// proxyStats counts what the proxy carried, for the control surface.
type proxyStats struct {
	mu           sync.Mutex
	tunnels      map[string]int
	forwards     map[string]int
	authFailures int
}

func newProxyStats() *proxyStats {
	return &proxyStats{tunnels: map[string]int{}, forwards: map[string]int{}}
}

func (s *proxyStats) tunnel(target string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tunnels[target]++
}

func (s *proxyStats) forward(target string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.forwards[target]++
}

func (s *proxyStats) authFailed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authFailures++
}

func (s *proxyStats) snapshot() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	tunnels := map[string]int{}
	for k, v := range s.tunnels {
		tunnels[k] = v
	}
	forwards := map[string]int{}
	for k, v := range s.forwards {
		forwards[k] = v
	}
	return map[string]any{
		"tunnels":       tunnels,
		"forwards":      forwards,
		"auth_failures": s.authFailures,
	}
}

// proxies is the stats the running proxy keeps; set once in main.
var proxies = newProxyStats()

// proxyHandler is an HTTP proxy: CONNECT tunnels (any :443 goes to the
// WebSocket endpoint at wsTarget, everything else where it asks) and
// absolute-form forwarding for plain-HTTP requests. When auth ("user:pass")
// is set, a request without matching Basic Proxy-Authorization gets 407.
func proxyHandler(auth, wsTarget string, stats *proxyStats) http.Handler {
	want := ""
	if auth != "" {
		want = "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
	}
	direct := &http.Transport{Proxy: nil}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if want != "" && r.Header.Get("Proxy-Authorization") != want {
			stats.authFailed()
			log.Printf("proxy: %s %s -> 407", r.Method, r.Host)
			w.Header().Set("Proxy-Authenticate", `Basic realm="stub-proxy"`)
			http.Error(w, "proxy authentication required", http.StatusProxyAuthRequired)
			return
		}
		if r.Method == http.MethodConnect {
			tunnel(w, r, wsTarget, stats)
			return
		}
		if r.URL.Host == "" {
			http.Error(w, "not a proxy request", http.StatusBadRequest)
			return
		}
		stats.forward(r.URL.Host)
		out := r.Clone(r.Context())
		out.RequestURI = ""
		out.Header.Del("Proxy-Authorization")
		out.Header.Del("Proxy-Connection")
		resp, err := direct.RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = resp.Body.Close() }()
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		log.Printf("proxy: %s %s -> %d", r.Method, r.URL, resp.StatusCode)
	})
}

func tunnel(w http.ResponseWriter, r *http.Request, wsTarget string, stats *proxyStats) {
	target := r.Host
	dial := target
	if _, port, err := net.SplitHostPort(target); err == nil && port == "443" {
		dial = wsTarget
	}
	upstream, err := net.DialTimeout("tcp", dial, 10*time.Second)
	if err != nil {
		log.Printf("proxy: CONNECT %s (via %s) -> 502: %v", target, dial, err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		http.Error(w, "cannot tunnel", http.StatusInternalServerError)
		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	stats.tunnel(target)
	log.Printf("proxy: CONNECT %s (via %s) -> 200", target, dial)
	_, _ = client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
	go pipe(upstream, client, buffered)
}

// pipe copies both ways until either side closes, flushing whatever the
// client had already sent past the CONNECT request.
func pipe(upstream, client net.Conn, buffered *bufio.ReadWriter) {
	defer func() { _ = upstream.Close(); _ = client.Close() }()
	done := make(chan struct{}, 2)
	go func() {
		if n := buffered.Reader.Buffered(); n > 0 {
			pending, _ := buffered.Peek(n)
			_, _ = upstream.Write(pending)
		}
		_, _ = io.Copy(upstream, client)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, upstream)
		done <- struct{}{}
	}()
	<-done
}

// handleProxyStats serves GET /_control/proxy.
func handleProxyStats(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(proxies.snapshot())
}

// handleDirectMQTT serves POST /_control/direct-mqtt?accept=false|true and GET.
func handleDirectMQTT(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		switch r.URL.Query().Get("accept") {
		case "false":
			refuseDirect.Store(true)
		case "true":
			refuseDirect.Store(false)
		default:
			http.Error(w, "accept must be true or false", http.StatusBadRequest)
			return
		}
		log.Printf("direct MQTT on the TLS listener: accept=%t", !refuseDirect.Load())
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]bool{"accept": !refuseDirect.Load()})
}
