//go:build integration

// The hub half of the fixture (sc-117887): a minimal Azure IoT Hub stand-in
// that can *deliver* cloud-to-device messages, plus a stand-in Rewst engine.
//
// The broker keeps, per device (MQTT client id), a queue of messages waiting
// for a subscriber and a set of messages in flight (published at QoS 1, PUBACK
// not yet seen). A SUBSCRIBE that is acknowledged registers the connection as
// the device's live subscriber and flushes the queue to it; a PUBACK retires
// the in-flight message; a connection that closes with messages in flight puts
// them back on the queue, so the next subscriber receives them again with the
// DUP flag set - the redelivery the agent's command journal (sc-115628) is
// built around.
//
// The engine answers the two endpoints the suite's send-command action and the
// agent talk to: the trigger (multipart form, device_id + commands) enqueues a
// command for the device and blocks until the matching postback arrives,
// answering in the real engine's envelope or with its 408 timeout body; the
// postback receiver records each result once and refuses a duplicate with the
// real engine's 400 "fulfilled" body. A control surface lets the harness
// enqueue without waiting and read what has been posted back.
package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/encoding/unicode"
)

// pendingMessage is one C2D message the broker owes a device.
type pendingMessage struct {
	ID       string          `json:"id"`
	Payload  json.RawMessage `json:"payload"`
	Attempts int             `json:"attempts"`
}

// deviceState is everything the broker tracks for one device.
type deviceState struct {
	queue      []*pendingMessage
	inflight   map[uint16]*pendingMessage
	subscriber *clientConn
	delivered  int
	nextPacket uint16
	// conns is every accepted connection claiming this device id, subscribed
	// or not, so an offline control can drop all of them.
	conns map[*clientConn]struct{}
	// offlineUntil, while in the future, makes the broker refuse the device's
	// CONNECT (close without CONNACK) - the hermetic stand-in for severing the
	// device's network path to the hub (sc-126319).
	offlineUntil time.Time
	connects     int
	// pubacks counts every PUBACK the device sent, honoured or not.
	pubacks int
	// loseNextPuback makes the broker ignore the device's next PUBACK: the
	// message stays in flight, is requeued when the connection drops, and is
	// redelivered with DUP on the next subscribe - the lost-acknowledgement
	// case the command journal's tombstones exist for (sc-126322).
	loseNextPuback bool
}

// clientConn is one accepted MQTT connection.
type clientConn struct {
	conn     net.Conn
	writeMu  sync.Mutex
	clientID string
}

func (c *clientConn) write(packetType, flags byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return writePacket(c.conn, packetType, flags, payload)
}

// postback is one result the engine received.
type postback struct {
	PostID     string          `json:"post_id"`
	Body       json.RawMessage `json:"body"`
	ReceivedAt time.Time       `json:"received_at"`
}

// hub is the shared state behind the broker and the engine.
type hub struct {
	mu        sync.Mutex
	devices   map[string]*deviceState
	postbacks map[string]*postback
	waiters   map[string]chan *postback
	// postbackBase is what the engine puts in $post_url for PowerShell
	// commands, e.g. https://127.0.0.1:8443.
	postbackBase  string
	engineTimeout time.Duration
	// responseScripts holds, per post_id, the HTTP statuses the engine answers
	// the next postbacks with before behaving normally; attempts counts every
	// postback the engine saw for a post_id, accepted or not (sc-126322).
	responseScripts map[string][]int
	attempts        map[string]int
}

func newHub(postbackBase string, engineTimeout time.Duration) *hub {
	return &hub{
		devices:         map[string]*deviceState{},
		postbacks:       map[string]*postback{},
		waiters:         map[string]chan *postback{},
		postbackBase:    postbackBase,
		engineTimeout:   engineTimeout,
		responseScripts: map[string][]int{},
		attempts:        map[string]int{},
	}
}

func (h *hub) device(id string) *deviceState {
	d, ok := h.devices[id]
	if !ok {
		d = &deviceState{inflight: map[uint16]*pendingMessage{}, conns: map[*clientConn]struct{}{}}
		h.devices[id] = d
	}
	return d
}

// enqueue owes device a message and delivers it at once if a subscriber is live.
func (h *hub) enqueue(deviceID string, payload json.RawMessage) *pendingMessage {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.device(deviceID)
	m := &pendingMessage{ID: newID(), Payload: payload}
	d.queue = append(d.queue, m)
	h.flushLocked(deviceID, d)
	return m
}

// flushLocked publishes everything queued to the live subscriber, if any.
func (h *hub) flushLocked(deviceID string, d *deviceState) {
	if d.subscriber == nil {
		return
	}
	for len(d.queue) > 0 {
		m := d.queue[0]
		d.queue = d.queue[1:]
		d.nextPacket++
		if d.nextPacket == 0 {
			d.nextPacket = 1
		}
		pid := d.nextPacket
		flags := byte(0x02) // QoS 1
		if m.Attempts > 0 {
			flags |= 0x08 // DUP
		}
		m.Attempts++
		d.inflight[pid] = m
		topic := fmt.Sprintf("devices/%s/messages/devicebound/", deviceID)
		body := make([]byte, 0, 2+len(topic)+2+len(m.Payload))
		body = append(body, byte(len(topic)>>8), byte(len(topic)))
		body = append(body, topic...)
		body = append(body, byte(pid>>8), byte(pid))
		body = append(body, m.Payload...)
		if err := d.subscriber.write(pktPublish, flags, body); err != nil {
			log.Printf(
				"[%s] PUBLISH to %s failed: %v; requeued",
				d.subscriber.conn.RemoteAddr(), deviceID, err,
			)
			delete(d.inflight, pid)
			d.queue = append([]*pendingMessage{m}, d.queue...)
			return
		}
		log.Printf(
			"[%s] PUBLISH qos1 pid=%d dup=%t to %s (%s)",
			d.subscriber.conn.RemoteAddr(),
			pid,
			m.Attempts > 1,
			deviceID,
			m.ID,
		)
	}
}

// connected records a CONNECT from c for deviceID and reports whether the
// broker accepts it. A device taken offline through the control surface is
// refused until the offline window lapses or it is put back online.
func (h *hub) connected(deviceID string, c *clientConn) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.device(deviceID)
	if time.Now().Before(d.offlineUntil) {
		return false
	}
	d.conns[c] = struct{}{}
	d.connects++
	return true
}

// setOffline drops every connection deviceID holds and refuses its CONNECTs
// for the next d. Unacknowledged in-flight messages are requeued by the
// connections' own disconnect handling, exactly as on a real network cut.
func (h *hub) setOffline(deviceID string, dur time.Duration) int {
	h.mu.Lock()
	d := h.device(deviceID)
	d.offlineUntil = time.Now().Add(dur)
	conns := make([]*clientConn, 0, len(d.conns))
	for c := range d.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	for _, c := range conns {
		_ = c.conn.Close()
	}
	log.Printf("%s: offline for %s; dropped %d connection(s)", deviceID, dur, len(conns))
	return len(conns)
}

// setOnline lets deviceID connect again.
func (h *hub) setOnline(deviceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.device(deviceID).offlineUntil = time.Time{}
	log.Printf("%s: online", deviceID)
}

// subscribed registers c as deviceID's live subscriber and flushes to it.
func (h *hub) subscribed(deviceID string, c *clientConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.device(deviceID)
	d.subscriber = c
	h.flushLocked(deviceID, d)
}

// puback retires an in-flight message.
func (h *hub) puback(deviceID string, pid uint16) {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.device(deviceID)
	d.pubacks++
	m, ok := d.inflight[pid]
	if !ok {
		return
	}
	if d.loseNextPuback {
		d.loseNextPuback = false
		log.Printf(
			"PUBACK pid=%d from %s (%s) -> lost on purpose; message stays in flight",
			pid, deviceID, m.ID,
		)
		return
	}
	delete(d.inflight, pid)
	d.delivered++
	log.Printf("PUBACK pid=%d from %s (%s) -> delivered", pid, deviceID, m.ID)
}

// loseNextPuback arms the device's next PUBACK to be ignored.
func (h *hub) loseNextPuback(deviceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.device(deviceID).loseNextPuback = true
	log.Printf("%s: the next PUBACK will be lost", deviceID)
}

// disconnected forgets c as a subscriber and returns its in-flight messages to
// the queue, so the next subscriber gets them again (DUP).
func (h *hub) disconnected(deviceID string, c *clientConn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	d, ok := h.devices[deviceID]
	if !ok {
		return
	}
	delete(d.conns, c)
	if d.subscriber == c {
		d.subscriber = nil
	}
	for pid, m := range d.inflight {
		delete(d.inflight, pid)
		d.queue = append([]*pendingMessage{m}, d.queue...)
		log.Printf(
			"%s: pid=%d (%s) unacknowledged at disconnect -> requeued for redelivery",
			deviceID,
			pid,
			m.ID,
		)
	}
}

type deviceSnapshot struct {
	Queued    int  `json:"queued"`
	Inflight  int  `json:"inflight"`
	Delivered int  `json:"delivered"`
	Connects  int  `json:"connects"`
	Connected int  `json:"connected"`
	Offline   bool `json:"offline"`
	Pubacks   int  `json:"pubacks"`
	// LoseNextPuback reports whether the next PUBACK is still armed to be lost.
	LoseNextPuback bool `json:"lose_next_puback"`
}

func (h *hub) snapshot(deviceID string) deviceSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	d := h.device(deviceID)
	return deviceSnapshot{
		Queued:         len(d.queue),
		Inflight:       len(d.inflight),
		Delivered:      d.delivered,
		Connects:       d.connects,
		Connected:      len(d.conns),
		Offline:        time.Now().Before(d.offlineUntil),
		Pubacks:        d.pubacks,
		LoseNextPuback: d.loseNextPuback,
	}
}

// recordPostback stores the first result for a post_id and reports whether it
// was the first. Waiters blocked in the trigger are released.
func (h *hub) recordPostback(postID string, body json.RawMessage) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, dup := h.postbacks[postID]; dup {
		return false
	}
	p := &postback{PostID: postID, Body: body, ReceivedAt: time.Now()}
	h.postbacks[postID] = p
	if ch, ok := h.waiters[postID]; ok {
		ch <- p
		delete(h.waiters, postID)
	}
	return true
}

func (h *hub) waitFor(postID string) <-chan *postback {
	h.mu.Lock()
	defer h.mu.Unlock()
	ch := make(chan *postback, 1)
	if p, ok := h.postbacks[postID]; ok {
		ch <- p
		return ch
	}
	h.waiters[postID] = ch
	return ch
}

func (h *hub) allPostbacks() []*postback {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]*postback, 0, len(h.postbacks))
	for _, p := range h.postbacks {
		out = append(out, p)
	}
	return out
}

// scriptResponses replaces the statuses the engine answers postID's next
// postbacks with. A 200 in the list, or running out of list, means the normal
// path (record once, 400 "fulfilled" for a duplicate).
func (h *hub) scriptResponses(postID string, statuses []int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.responseScripts[postID] = append([]int(nil), statuses...)
	log.Printf("engine: postbacks for %s scripted as %v", postID, statuses)
}

// nextScriptedStatus counts a postback attempt for postID and pops the next
// scripted status, or 0 when the normal path applies.
func (h *hub) nextScriptedStatus(postID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.attempts[postID]++
	script := h.responseScripts[postID]
	if len(script) == 0 {
		return 0
	}
	status := script[0]
	h.responseScripts[postID] = script[1:]
	if status == http.StatusOK {
		return 0
	}
	return status
}

type postbackResponsesSnapshot struct {
	PostID    string `json:"post_id"`
	Attempts  int    `json:"attempts"`
	Remaining []int  `json:"remaining"`
	Recorded  bool   `json:"recorded"`
}

func (h *hub) postbackResponses(postID string) postbackResponsesSnapshot {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, recorded := h.postbacks[postID]
	return postbackResponsesSnapshot{
		PostID:    postID,
		Attempts:  h.attempts[postID],
		Remaining: append([]int{}, h.responseScripts[postID]...),
		Recorded:  recorded,
	}
}

// ── engine ───────────────────────────────────────────────────────────────────

// engineMux serves the trigger, the control surface and (read-only) postbacks
// over plain HTTP; the postback receiver is served over TLS by postbackMux.
func (h *hub) engineMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/webhooks/custom/trigger/", h.handleTrigger)
	mux.HandleFunc("/_control/enqueue", h.handleEnqueue)
	mux.HandleFunc("/_control/devices/", h.handleDevice)
	mux.HandleFunc("/_control/postbacks", h.handlePostbacks)
	mux.HandleFunc("/_control/postback-responses", h.handlePostbackResponses)
	return mux
}

func (h *hub) postbackMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/webhooks/custom/action/", h.handlePostback)
	return mux
}

// handleTrigger mirrors the Rewst send-command workflow: dispatch, then block
// for the device's postback up to the engine's ceiling.
func (h *hub) handleTrigger(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		http.Error(w, `{"error":"bad form"}`, http.StatusBadRequest)
		return
	}
	deviceID := r.FormValue("device_id")
	commands := r.FormValue("commands")
	if deviceID == "" || commands == "" {
		http.Error(w, `{"error":"device_id and commands are required"}`, http.StatusBadRequest)
		return
	}
	// The suite passes the form value JSON-quoted (`"echo \"hello world\""`),
	// as the real engine expects; accept both that and a bare string.
	var decoded string
	if err := json.Unmarshal([]byte(commands), &decoded); err == nil {
		commands = decoded
	}
	postID := newID()
	// The real engine's typed-PowerShell wrapper defines $post_url for scripts
	// that post their own result; a script that references it gets the same.
	if strings.Contains(commands, "$post_url") {
		commands = fmt.Sprintf(
			"$post_url = \"%s/webhooks/custom/action/%s\"\n%s",
			h.postbackBase,
			postID,
			commands,
		)
	}
	ch := h.waitFor(postID)
	h.enqueue(deviceID, commandPayload(postID, commands))
	log.Printf("engine: trigger for %s -> post_id %s", deviceID, postID)

	select {
	case p := <-ch:
		result := p.Body
		if len(result) == 0 || !json.Valid(result) {
			result = json.RawMessage(`{}`)
		}
		env := map[string]any{
			"output": map[string]any{"output": map[string]any{
				"device_execution_results": "",
				"command_results":          result,
			}},
			"errors":  []any{},
			"options": []any{},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(env)
	case <-time.After(h.engineTimeout):
		log.Printf("engine: post_id %s timed out after %s", postID, h.engineTimeout)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestTimeout)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error":        "The workflow did not complete in a reasonable amount of time to receive results.",
			"execution_id": postID,
		})
	case <-r.Context().Done():
	}
}

// handlePostback is the agent's /webhooks/custom/action/{post_id}.
func (h *hub) handlePostback(w http.ResponseWriter, r *http.Request) {
	postID := strings.TrimPrefix(r.URL.Path, "/webhooks/custom/action/")
	body, _ := io.ReadAll(io.LimitReader(r.Body, 32<<20))
	if status := h.nextScriptedStatus(postID); status != 0 {
		log.Printf("engine: postback for %s -> %d (scripted)", postID, status)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"error":"scripted %d"}`, status)
		return
	}
	if !h.recordPostback(postID, body) {
		log.Printf("engine: duplicate postback for %s -> 400 fulfilled", postID)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"Webhook has already been fulfilled"}`))
		return
	}
	log.Printf("engine: postback for %s (%d bytes)", postID, len(body))
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{}`))
}

// commandPayload is the C2D message the real engine sends for a command: the
// script is UTF-16LE encoded and then base64-encoded in the "commands" field
// (the PowerShell -EncodedCommand convention), and the agent reverses both
// before executing. Plain base64 of UTF-8 ran as "捥潨∠敨汬⁯潷汲≤: command not
// found" - the bytes read as UTF-16 - and plain text failed the base64 decode.
func commandPayload(postID, commands string) json.RawMessage {
	msg, _ := json.Marshal(map[string]string{
		"post_id":  postID,
		"commands": encodeCommands(commands),
	})
	return msg
}

func encodeCommands(commands string) string {
	encoder := unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewEncoder()
	utf16Bytes, err := encoder.Bytes([]byte(commands))
	if err != nil {
		utf16Bytes = []byte(commands)
	}
	return base64.StdEncoding.EncodeToString(utf16Bytes)
}

// handleEnqueue lets the harness owe a device a message without waiting for a
// result - the way to queue many commands and then kill the agent. Either a
// plain {post_id, commands} pair (encoded here exactly as the trigger would) or
// a raw payload to deliver verbatim.
func (h *hub) handleEnqueue(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DeviceID string          `json:"device_id"`
		PostID   string          `json:"post_id"`
		Commands string          `json:"commands"`
		Payload  json.RawMessage `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.DeviceID == "" {
		http.Error(w, `{"error":"device_id is required"}`, http.StatusBadRequest)
		return
	}
	payload := req.Payload
	if len(payload) == 0 {
		if req.PostID == "" || req.Commands == "" {
			http.Error(
				w,
				`{"error":"post_id and commands, or a raw payload, are required"}`,
				http.StatusBadRequest,
			)
			return
		}
		payload = commandPayload(req.PostID, req.Commands)
	}
	m := h.enqueue(req.DeviceID, payload)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"id": m.ID})
}

// handleDevice serves /_control/devices/{id} (snapshot), and the offline and
// online controls at /_control/devices/{id}/offline?seconds=N and
// /_control/devices/{id}/online.
func (h *hub) handleDevice(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/_control/devices/")
	id, action, _ := strings.Cut(rest, "/")
	w.Header().Set("Content-Type", "application/json")
	switch action {
	case "offline":
		seconds := 30
		if s := r.URL.Query().Get("seconds"); s != "" {
			if n, err := strconv.Atoi(s); err == nil && n > 0 {
				seconds = n
			}
		}
		dropped := h.setOffline(id, time.Duration(seconds)*time.Second)
		_ = json.NewEncoder(w).
			Encode(map[string]any{"device_id": id, "offline_seconds": seconds, "dropped": dropped})
	case "online":
		h.setOnline(id)
		_ = json.NewEncoder(w).Encode(map[string]any{"device_id": id, "online": true})
	case "lose-next-puback":
		h.loseNextPuback(id)
		_ = json.NewEncoder(w).Encode(map[string]any{"device_id": id, "lose_next_puback": true})
	default:
		_ = json.NewEncoder(w).Encode(h.snapshot(id))
	}
}

// handlePostbackResponses scripts the engine's answers to a post_id's
// postbacks (POST {post_id, statuses:[503,503,200]}) and reports what the
// engine has seen for one (GET ?post_id=...): attempts, the statuses still
// scripted, and whether a result was recorded.
func (h *hub) handlePostbackResponses(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodPost:
		var req struct {
			PostID   string `json:"post_id"`
			Statuses []int  `json:"statuses"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PostID == "" {
			http.Error(w, `{"error":"post_id and statuses are required"}`, http.StatusBadRequest)
			return
		}
		h.scriptResponses(req.PostID, req.Statuses)
		_ = json.NewEncoder(w).Encode(h.postbackResponses(req.PostID))
	default:
		postID := r.URL.Query().Get("post_id")
		if postID == "" {
			http.Error(w, `{"error":"post_id is required"}`, http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(h.postbackResponses(postID))
	}
}

func (h *hub) handlePostbacks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(h.allPostbacks())
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// ── MQTT CONNECT parsing ─────────────────────────────────────────────────────

// connectClientID extracts the client identifier from a CONNECT payload:
// protocol name (length-prefixed), level, flags, keepalive, then client id.
func connectClientID(payload []byte) (string, error) {
	if len(payload) < 2 {
		return "", fmt.Errorf("short CONNECT")
	}
	n := int(payload[0])<<8 | int(payload[1])
	pos := 2 + n + 1 + 1 + 2 // name, level, flags, keepalive
	if len(payload) < pos+2 {
		return "", fmt.Errorf("short CONNECT variable header")
	}
	idLen := int(payload[pos])<<8 | int(payload[pos+1])
	pos += 2
	if len(payload) < pos+idLen {
		return "", fmt.Errorf("short CONNECT client id")
	}
	return string(payload[pos : pos+idLen]), nil
}
