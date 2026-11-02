//go:build integration

package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

// The proxy fixture carries paho's WebSocket transport - the agent's own - to
// the broker's WebSocket endpoint through an authenticated CONNECT tunnel
// (sc-129691), refuses a request without the credentials, and counts what it
// carried; the direct-MQTT switch refuses the TLS listener.
func TestFixture_MQTTOverWebSocketThroughTheProxy(t *testing.T) {
	_, engineURL, _, _, _ := startFixture(t)
	tlsConfig, caPEM, err := newTLSConfig("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)

	modeFile := t.TempDir() + "/mode"
	if err := writeFile(modeFile, modeAck); err != nil {
		t.Fatal(err)
	}
	ws := httptest.NewUnstartedServer(webSocketHandler(modeFile))
	ws.TLS = tlsConfig.Clone()
	ws.StartTLS()
	t.Cleanup(ws.Close)
	wsAddr := ws.Listener.Addr().String()

	stats := newProxyStats()
	proxy := httptest.NewServer(proxyHandler("agent:s3cret-proxy", wsAddr, stats))
	t.Cleanup(proxy.Close)
	proxyURL, _ := url.Parse(proxy.URL)
	proxyURL.User = url.UserPassword("agent", "s3cret-proxy")

	connect := func(p *url.URL) (mqtt.Client, error) {
		opts := mqtt.NewClientOptions().
			AddBroker("wss://127.0.0.1:443" + webSocketPath).
			SetClientID("dev-proxy").
			SetTLSConfig(&tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}).
			SetAutoReconnect(false).
			SetConnectTimeout(5 * time.Second).
			SetWebsocketOptions(&mqtt.WebsocketOptions{
				Proxy: func(*http.Request) (*url.URL, error) { return p, nil },
			})
		c := mqtt.NewClient(opts)
		tok := c.Connect()
		if !tok.WaitTimeout(10 * time.Second) {
			return nil, net.ErrClosed
		}
		return c, tok.Error()
	}

	c, err := connect(proxyURL)
	if err != nil {
		t.Fatalf("connect through the proxy: %v", err)
	}
	defer c.Disconnect(100)
	sub := c.Subscribe(
		"devices/dev-proxy/messages/devicebound/#",
		1,
		func(mqtt.Client, mqtt.Message) {},
	)
	if !sub.WaitTimeout(5*time.Second) || sub.Error() != nil {
		t.Fatalf("subscribe over WebSocket: %v", sub.Error())
	}
	snap := stats.snapshot()
	if snap["tunnels"].(map[string]int)["127.0.0.1:443"] != 1 {
		t.Errorf("proxy stats %v", snap)
	}

	// Without the credentials the proxy answers 407 and nothing connects.
	bare, _ := url.Parse(proxy.URL)
	if c2, err := connect(bare); err == nil {
		c2.Disconnect(100)
		t.Error("connected through the proxy without credentials")
	}
	if stats.snapshot()["auth_failures"].(int) != 1 {
		t.Errorf("auth failures %v", stats.snapshot())
	}

	// The control surface switches direct MQTT off and reports it.
	hc := &http.Client{Timeout: 5 * time.Second}
	resp, err := hc.Post(engineURL+"/_control/direct-mqtt?accept=false", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]bool
	_ = json.NewDecoder(resp.Body).Decode(&state)
	if state["accept"] || !refuseDirect.Load() {
		t.Errorf("direct MQTT still accepted: %v", state)
	}
	if resp, err := hc.Post(engineURL+"/_control/direct-mqtt?accept=maybe", "", nil); err != nil ||
		resp.StatusCode != http.StatusBadRequest {
		t.Errorf("a bad accept value: %v %v", err, resp)
	}
	if _, err := hc.Post(engineURL+"/_control/direct-mqtt?accept=true", "", nil); err != nil {
		t.Fatal(err)
	}
	if refuseDirect.Load() {
		t.Error("direct MQTT was not switched back on")
	}
}
