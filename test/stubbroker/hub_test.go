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

// The hermetic stand-in for cutting the device's network path (sc-126319):
// taking a device offline drops its live connection, refuses its CONNECT
// while offline, and lets it back once online - the agent then exercises the
// same connection-lost, backoff and resubscribe path as on a real cut.
func TestFixture_OfflineDropsRefusesThenReadmits(t *testing.T) {
	brokerAddr, engineURL, _, clientTLS, h := startFixture(t)
	const device = "offline-device"
	client := connectDevice(t, brokerAddr, device, clientTLS, func(mqtt.Client, mqtt.Message) {})

	resp, err := http.Post(engineURL+"/_control/devices/"+device+"/offline?seconds=60", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	deadline := time.Now().Add(5 * time.Second)
	for client.IsConnectionOpen() && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond) // sleep-ok: poll interval
	}
	if client.IsConnectionOpen() {
		t.Fatal("the live connection survived the offline control")
	}
	if snap := h.snapshot(device); !snap.Offline || snap.Connected != 0 {
		t.Fatalf("snapshot after offline = %+v", snap)
	}

	// A fresh CONNECT is refused while offline.
	opts := mqtt.NewClientOptions().AddBroker("tls://" + brokerAddr).SetClientID(device).
		SetTLSConfig(clientTLS).SetAutoReconnect(false).SetConnectTimeout(3 * time.Second)
	retry := mqtt.NewClient(opts)
	if tok := retry.Connect(); tok.WaitTimeout(5*time.Second) && tok.Error() == nil {
		t.Fatal("CONNECT was accepted while the device was offline")
	}

	resp, err = http.Post(engineURL+"/_control/devices/"+device+"/online", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	again := connectDevice(t, brokerAddr, device, clientTLS, func(mqtt.Client, mqtt.Message) {})
	defer again.Disconnect(100)
	if snap := h.snapshot(device); snap.Offline || snap.Connected != 1 || snap.Connects < 2 {
		t.Fatalf("snapshot after online = %+v", snap)
	}
}

// A PUBACK the broker is told to lose leaves the message in flight, so the
// disconnect requeues it and the next subscriber receives it again with DUP -
// the lost-acknowledgement redelivery the command journal de-duplicates by
// tombstone (sc-126322). The snapshot counts both PUBACKs.
func TestFixture_LostPubackRedeliversWithDup(t *testing.T) {
	brokerAddr, engineURL, _, clientTLS, h := startFixture(t)
	hc := &http.Client{Timeout: 5 * time.Second}
	resp, err := hc.Post(
		engineURL+"/_control/devices/dev-5/lose-next-puback",
		"application/json",
		nil,
	)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("lose-next-puback: %v %v", err, resp)
	}
	if s := h.snapshot("dev-5"); !s.LoseNextPuback {
		t.Fatalf("snapshot does not show the armed loss: %+v", s)
	}

	first := make(chan mqtt.Message, 1)
	c1 := connectDevice(
		t,
		brokerAddr,
		"dev-5",
		clientTLS,
		func(_ mqtt.Client, m mqtt.Message) { first <- m },
	)
	resp, err = hc.Post(
		engineURL+"/_control/enqueue",
		"application/json",
		bytes.NewBufferString(`{"device_id":"dev-5","post_id":"p5","commands":"echo hi"}`),
	)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("enqueue: %v %v", err, resp)
	}
	select {
	case m := <-first:
		m.Ack() // the PUBACK the broker loses
	case <-time.After(5 * time.Second):
		t.Fatal("first delivery never arrived")
	}
	deadline := time.Now().Add(5 * time.Second)
	for h.snapshot("dev-5").Pubacks < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond) // sleep-ok: poll interval
	}
	if s := h.snapshot("dev-5"); s.Pubacks != 1 || s.Inflight != 1 || s.Delivered != 0 ||
		s.LoseNextPuback {
		t.Fatalf(
			"after the lost PUBACK: %+v, want it counted, the message still in flight, the arm cleared",
			s,
		)
	}
	c1.Disconnect(0)

	second := make(chan mqtt.Message, 1)
	c2 := connectDevice(
		t,
		brokerAddr,
		"dev-5",
		clientTLS,
		func(_ mqtt.Client, m mqtt.Message) { second <- m },
	)
	defer c2.Disconnect(100)
	select {
	case m := <-second:
		if !m.Duplicate() {
			t.Error("redelivery after the lost PUBACK not flagged DUP")
		}
		m.Ack()
	case <-time.After(5 * time.Second):
		t.Fatal("message was not redelivered after the lost PUBACK")
	}
	deadline = time.Now().Add(5 * time.Second)
	for h.snapshot("dev-5").Delivered != 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond) // sleep-ok: poll interval
	}
	if s := h.snapshot("dev-5"); s.Delivered != 1 || s.Pubacks != 2 || s.Inflight != 0 ||
		s.Queued != 0 {
		t.Errorf(
			"after the honoured PUBACK: %+v, want delivered=1 pubacks=2 and nothing pending",
			s,
		)
	}
}

// Scripted postback responses: the engine answers the scripted statuses in
// order without recording, then records the first accepted result and refuses
// the duplicate; every attempt is counted (sc-126322).
func TestFixture_ScriptedPostbackResponses(t *testing.T) {
	_, engineURL, postbackBase, clientTLS, h := startFixture(t)
	hc := &http.Client{Timeout: 5 * time.Second}
	resp, err := hc.Post(
		engineURL+"/_control/postback-responses",
		"application/json",
		bytes.NewBufferString(`{"post_id":"p6","statuses":[503,500,200]}`),
	)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("script: %v %v", err, resp)
	}
	want := []int{503, 500, 200, 400}
	for i, status := range want {
		if got := postResult(t, clientTLS, postbackBase, "p6", `{"output":"x"}`); got != status {
			t.Errorf("postback %d: status %d, want %d", i+1, got, status)
		}
	}
	snap := h.postbackResponses("p6")
	if snap.Attempts != 4 || len(snap.Remaining) != 0 || !snap.Recorded {
		t.Errorf("snapshot %+v, want attempts=4, nothing remaining, recorded", snap)
	}
	if n := len(h.allPostbacks()); n != 1 {
		t.Errorf("engine holds %d postbacks, want exactly the one accepted result", n)
	}
	// The GET form answers the same snapshot over HTTP, including for a post_id
	// that was never scripted.
	r, err := hc.Get(engineURL + "/_control/postback-responses?post_id=p6")
	if err != nil || r.StatusCode != 200 {
		t.Fatalf("GET: %v %v", err, r)
	}
	var got postbackResponsesSnapshot
	if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Attempts != 4 || !got.Recorded {
		t.Errorf("GET snapshot %+v", got)
	}
	if u := h.postbackResponses("never"); u.Attempts != 0 || u.Recorded {
		t.Errorf("unscripted post_id snapshot %+v", u)
	}
}

// A reported-properties PATCH is kept per device and served back merged, so
// the harness can read what the agent reports about itself (sc-129687).
func TestFixture_TwinReportedPropertiesAreStoredAndMerged(t *testing.T) {
	brokerAddr, engineURL, _, clientTLS, h := startFixture(t)
	c := connectDevice(t, brokerAddr, "dev-8", clientTLS, func(mqtt.Client, mqtt.Message) {})
	defer c.Disconnect(100)
	topic := "$iothub/twin/PATCH/properties/reported/?$rid=1"
	for _, payload := range []string{
		`{"agent_version":"v1.6.1","health":{"journal":{"pending":1}}}`,
		`{"health":{"journal":{"pending":0,"degraded":false}}}`,
	} {
		tok := c.Publish(topic, 0, false, payload)
		if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
			t.Fatalf("publish: %v", tok.Error())
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(h.twin("dev-8")) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond) // sleep-ok: poll interval
	}
	hc := &http.Client{Timeout: 5 * time.Second}
	resp, err := hc.Get(engineURL + "/_control/devices/dev-8/twin")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("twin: %v %v", err, resp)
	}
	var got struct {
		Reported map[string]json.RawMessage `json:"reported"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if string(got.Reported["agent_version"]) != `"v1.6.1"` {
		t.Errorf("agent_version lost by the merge: %s", got.Reported["agent_version"])
	}
	if !strings.Contains(string(got.Reported["health"]), `"degraded":false`) {
		t.Errorf("the later PATCH did not replace health: %s", got.Reported["health"])
	}
	enqueue := bytes.NewBufferString(`{"device_id":"dev-8","post_id":"p8","commands":"echo"}`)
	if _, err := hc.Post(engineURL+"/_control/enqueue", "application/json", enqueue); err != nil {
		t.Fatal(err)
	}
	if s := h.snapshot("dev-8"); s.Queued+s.Inflight+s.Delivered != 1 {
		t.Errorf("the twin store disturbed message delivery: %+v", s)
	}
}

// The fixture plays IoT Hub's twin for runtime settings (sc-129688): a GET is
// answered with desired and reported, a desired change set through the
// control surface is delivered as a PATCH with its $version, a reported PATCH
// is answered with 204, and reported properties merge key by key.
func TestFixture_TwinDesiredPropertiesGetPatchAndMerge(t *testing.T) {
	brokerAddr, engineURL, _, clientTLS, h := startFixture(t)
	c := connectDevice(t, brokerAddr, "dev-9", clientTLS, func(mqtt.Client, mqtt.Message) {})
	defer c.Disconnect(100)
	got := make(chan mqtt.Message, 16)
	sub := c.SubscribeMultiple(map[string]byte{
		"$iothub/twin/PATCH/properties/desired/#": 0,
		"$iothub/twin/res/#":                      0,
	}, func(_ mqtt.Client, m mqtt.Message) { got <- m })
	if !sub.WaitTimeout(5*time.Second) || sub.Error() != nil {
		t.Fatalf("twin subscribe: %v", sub.Error())
	}
	next := func() mqtt.Message {
		t.Helper()
		select {
		case m := <-got:
			return m
		case <-time.After(5 * time.Second):
			t.Fatal("no twin message")
			return nil
		}
	}
	hc := &http.Client{Timeout: 5 * time.Second}
	setDesired := func(body string) {
		t.Helper()
		resp, err := hc.Post(
			engineURL+"/_control/devices/dev-9/desired",
			"application/json",
			strings.NewReader(body),
		)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("desired: %v %v", err, resp)
		}
	}

	// Set before the GET: the GET carries it.
	setDesired(`{"command_timeout_seconds": 5}`)
	if m := next(); !strings.HasPrefix(
		m.Topic(),
		"$iothub/twin/PATCH/properties/desired/?$version=1",
	) ||
		!strings.Contains(string(m.Payload()), `"command_timeout_seconds":5`) ||
		!strings.Contains(string(m.Payload()), `"$version":1`) {
		t.Fatalf("desired PATCH %s %s", m.Topic(), m.Payload())
	}
	if tok := c.Publish("$iothub/twin/GET/?$rid=1001", 0, false, []byte{}); !tok.WaitTimeout(
		5 * time.Second,
	) {
		t.Fatal("GET publish")
	}
	m := next()
	if m.Topic() != "$iothub/twin/res/200/?$rid=1001" {
		t.Fatalf("GET answer topic %s", m.Topic())
	}
	var doc struct {
		Desired map[string]json.RawMessage `json:"desired"`
	}
	if err := json.Unmarshal(m.Payload(), &doc); err != nil ||
		string(
			doc.Desired["$version"],
		) != "1" || string(doc.Desired["command_timeout_seconds"]) != "5" {
		t.Fatalf("GET answer %s: %v", m.Payload(), err)
	}

	// A reported PATCH is answered with 204, and per-key objects accumulate.
	for _, payload := range []string{
		`{"settings_ack":{"command_timeout_seconds":{"status":"applied"}}}`,
		`{"settings_ack":{"worker_count":{"status":"rejected"}}}`,
	} {
		tok := c.Publish("$iothub/twin/PATCH/properties/reported/?$rid=1", 0, false, payload)
		if !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
			t.Fatalf("reported publish: %v", tok.Error())
		}
		if m := next(); !strings.HasPrefix(m.Topic(), "$iothub/twin/res/204/?$rid=1") {
			t.Fatalf("reported PATCH answer %s", m.Topic())
		}
	}
	ack := string(h.twin("dev-9")["settings_ack"])
	if !strings.Contains(ack, "command_timeout_seconds") || !strings.Contains(ack, "worker_count") {
		t.Errorf("reported properties did not merge key by key: %s", ack)
	}

	// null removes a desired key.
	setDesired(`{"command_timeout_seconds": null, "logging_level": "debug"}`)
	_ = next()
	resp, err := hc.Get(engineURL + "/_control/devices/dev-9/desired")
	if err != nil {
		t.Fatal(err)
	}
	var read struct {
		Version int64                      `json:"version"`
		Desired map[string]json.RawMessage `json:"desired"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&read); err != nil {
		t.Fatal(err)
	}
	if read.Version != 2 || read.Desired["command_timeout_seconds"] != nil ||
		string(read.Desired["logging_level"]) != `"debug"` {
		t.Errorf("desired after removal: %+v", read)
	}
}
