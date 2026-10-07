//go:build integration

package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"golang.org/x/text/encoding/unicode"
)

// startFixture runs the broker, engine and postback receiver on loopback ports
// and returns a client TLS config that trusts the fixture's CA.
func startFixture(
	t *testing.T,
) (brokerAddr, engineURL, postbackBase string, clientTLS *tls.Config, h *hub) {
	t.Helper()
	tlsConfig, caPEM, err := newTLSConfig("127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(caPEM)
	clientTLS = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	brokerAddr = ln.Addr().String()

	engineLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	postbackLn, err := tls.Listen("tcp", "127.0.0.1:0", tlsConfig.Clone())
	if err != nil {
		t.Fatal(err)
	}
	engineURL = "http://" + engineLn.Addr().String()
	postbackBase = "https://" + postbackLn.Addr().String()

	h = newHub(postbackBase, 5*time.Second)
	broker = h
	modeFile := t.TempDir() + "/mode"
	if err := writeFile(modeFile, modeAck); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go serve(c, modeFile)
		}
	}()
	go func() { _ = http.Serve(engineLn, h.engineMux()) }()
	go func() { _ = http.Serve(postbackLn, h.postbackMux()) }()
	t.Cleanup(func() { _ = engineLn.Close(); _ = postbackLn.Close() })
	return
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o600)
}

// connectDevice connects a paho client as deviceID and subscribes to its C2D
// topic. Auto-ack is disabled so a test can decide whether to acknowledge.
func connectDevice(
	t *testing.T,
	brokerAddr, deviceID string,
	clientTLS *tls.Config,
	onMessage mqtt.MessageHandler,
) mqtt.Client {
	t.Helper()
	opts := mqtt.NewClientOptions().
		AddBroker("tls://" + brokerAddr).
		SetClientID(deviceID).
		SetTLSConfig(clientTLS).
		SetAutoReconnect(false).
		SetAutoAckDisabled(true).
		SetConnectTimeout(5 * time.Second)
	c := mqtt.NewClient(opts)
	if tok := c.Connect(); !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatalf("connect: %v", tok.Error())
	}
	sub := c.Subscribe("devices/"+deviceID+"/messages/devicebound/#", 1, onMessage)
	if !sub.WaitTimeout(5*time.Second) || sub.Error() != nil {
		t.Fatalf("subscribe: %v", sub.Error())
	}
	return c
}

func postResult(t *testing.T, clientTLS *tls.Config, postbackBase, postID, body string) int {
	t.Helper()
	hc := &http.Client{
		Transport: &http.Transport{TLSClientConfig: clientTLS},
		Timeout:   5 * time.Second,
	}
	resp, err := hc.Post(
		postbackBase+"/webhooks/custom/action/"+postID,
		"application/json",
		bytes.NewBufferString(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode
}

// trigger posts the suite's send-command form to the stand-in engine. It
// returns an error rather than failing the test because callers run it on a
// goroutine while the device side of the round trip is driven on the test's.
func trigger(engineURL, deviceID, commands string) (int, []byte, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("device_id", deviceID)
	_ = mw.WriteField("commands", commands)
	_ = mw.Close()
	req, _ := http.NewRequest(http.MethodPost, engineURL+"/webhooks/custom/trigger/a/b", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body, nil
}

// The round trip the suite's send-command performs: trigger -> C2D publish ->
// device posts back -> trigger answers in the engine's envelope.
func TestFixture_TriggerDeliversAndReturnsThePostback(t *testing.T) {
	brokerAddr, engineURL, postbackBase, clientTLS, _ := startFixture(t)
	received := make(chan mqtt.Message, 4)
	c := connectDevice(
		t,
		brokerAddr,
		"dev-1",
		clientTLS,
		func(_ mqtt.Client, m mqtt.Message) { received <- m },
	)
	defer c.Disconnect(100)

	done := make(chan struct{})
	var status int
	var body []byte
	var trigErr error
	go func() {
		status, body, trigErr = trigger(engineURL, "dev-1", `"echo \"hello world\""`)
		close(done)
	}()

	var m mqtt.Message
	select {
	case m = <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("device never received the C2D message")
	}
	var msg struct {
		PostID   string `json:"post_id"`
		Commands string `json:"commands"`
	}
	if err := json.Unmarshal(m.Payload(), &msg); err != nil || msg.PostID == "" {
		t.Fatalf("bad C2D payload %s: %v", m.Payload(), err)
	}
	decoded, err := decodeCommands(msg.Commands)
	if err != nil || decoded != `echo "hello world"` {
		t.Errorf(
			"commands = %q (decoded %q, %v); want the suite's quoting unwrapped, base64-encoded",
			msg.Commands,
			decoded,
			err,
		)
	}
	m.Ack()
	result := `{"error":"","output":"hello world\n"}`
	if code := postResult(t, clientTLS, postbackBase, msg.PostID, result); code != 200 {
		t.Fatalf("first postback: %d", code)
	}
	<-done
	if trigErr != nil {
		t.Fatal(trigErr)
	}
	if status != 200 {
		t.Fatalf("trigger status %d: %s", status, body)
	}
	var env struct {
		Output struct {
			Output struct {
				CommandResults struct {
					Output string `json:"output"`
				} `json:"command_results"`
			} `json:"output"`
		} `json:"output"`
	}
	if err := json.Unmarshal(body, &env); err != nil ||
		env.Output.Output.CommandResults.Output != "hello world\n" {
		t.Fatalf("trigger envelope not engine-shaped: %s", body)
	}
	if code := postResult(t, clientTLS, postbackBase, msg.PostID, `{}`); code != 400 {
		t.Errorf("duplicate postback = %d, want 400 fulfilled", code)
	}
}

// A message published to a device that disconnects without acknowledging is
// delivered again, flagged DUP, to the next subscriber - the broker behaviour
// the agent's journal (sc-115628) is built against.
func TestFixture_UnacknowledgedMessageIsRedeliveredWithDup(t *testing.T) {
	brokerAddr, engineURL, _, clientTLS, h := startFixture(t)
	first := make(chan mqtt.Message, 1)
	c1 := connectDevice(
		t,
		brokerAddr,
		"dev-2",
		clientTLS,
		func(_ mqtt.Client, m mqtt.Message) { first <- m },
	)

	hc := &http.Client{Timeout: 5 * time.Second}
	resp, err := hc.Post(
		engineURL+"/_control/enqueue",
		"application/json",
		bytes.NewBufferString(
			`{"device_id":"dev-2","post_id":"p1","commands":"sleep 1"}`,
		),
	)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("enqueue: %v %v", err, resp)
	}
	select {
	case m := <-first:
		if m.Duplicate() {
			t.Error("first delivery flagged DUP")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first delivery never arrived")
	}
	c1.Disconnect(0) // without acking
	snap := func() deviceSnapshot {
		deadline := time.Now().Add(5 * time.Second)
		for {
			s := h.snapshot("dev-2")
			if s.Inflight == 0 || time.Now().After(deadline) {
				return s
			}
			time.Sleep(10 * time.Millisecond) // sleep-ok: poll interval
		}
	}()
	if snap.Queued != 1 || snap.Inflight != 0 || snap.Delivered != 0 {
		t.Fatalf("after disconnect: %+v, want the message requeued", snap)
	}

	second := make(chan mqtt.Message, 1)
	c2 := connectDevice(
		t,
		brokerAddr,
		"dev-2",
		clientTLS,
		func(_ mqtt.Client, m mqtt.Message) { second <- m },
	)
	defer c2.Disconnect(100)
	select {
	case m := <-second:
		if !m.Duplicate() {
			t.Error("redelivery not flagged DUP")
		}
		m.Ack()
	case <-time.After(5 * time.Second):
		t.Fatal("message was not redelivered to the new subscriber")
	}
	deadline := time.Now().Add(5 * time.Second)
	for h.snapshot("dev-2").Delivered != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond) // sleep-ok: poll interval
	}
	if s := h.snapshot("dev-2"); s.Delivered != 1 || s.Queued != 0 || s.Inflight != 0 {
		t.Errorf("after ack: %+v, want delivered=1 and nothing pending", s)
	}
}

// A trigger whose device never answers gets the real engine's 408 shape, which
// send-command classifies as engine-timeout.
func TestFixture_TriggerTimesOutLikeTheEngine(t *testing.T) {
	_, engineURL, _, _, h := startFixture(t)
	h.engineTimeout = 300 * time.Millisecond
	status, body, err := trigger(engineURL, "dev-nobody", `"echo hi"`)
	if err != nil {
		t.Fatal(err)
	}
	if status != 408 ||
		!bytes.Contains(body, []byte("did not complete in a reasonable amount of time")) {
		t.Fatalf("got %d %s", status, body)
	}
}

// PowerShell commands that post their own result get $post_url defined for
// them, as the real engine's wrapper does.
func TestFixture_PostURLIsDefinedForSelfPostingScripts(t *testing.T) {
	brokerAddr, engineURL, postbackBase, clientTLS, _ := startFixture(t)
	received := make(chan mqtt.Message, 1)
	c := connectDevice(
		t,
		brokerAddr,
		"dev-3",
		clientTLS,
		func(_ mqtt.Client, m mqtt.Message) { received <- m },
	)
	defer c.Disconnect(100)
	go func() { _, _, _ = trigger(engineURL, "dev-3", `"Invoke-RestMethod -Uri $post_url -Method Post"`) }()
	select {
	case m := <-received:
		m.Ack()
		var msg struct{ Commands string }
		_ = json.Unmarshal(m.Payload(), &msg)
		decoded, _ := decodeCommands(msg.Commands)
		if !strings.HasPrefix(decoded, `$post_url = "`+postbackBase+`/webhooks/custom/action/`) {
			t.Fatalf("commands did not get $post_url defined: %q", decoded)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no delivery")
	}
}

// decodeCommands reverses encodeCommands the way the agent does: base64, then
// UTF-16LE.
func decodeCommands(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	decoder := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder()
	out, err := decoder.Bytes(raw)
	return string(out), err
}
