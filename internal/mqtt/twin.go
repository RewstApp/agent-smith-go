package mqtt

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/RewstApp/agent-smith-go/internal/utils"
)

// Device-twin topics (sc-129688). IoT Hub delivers a change to the twin's
// desired properties on the desired PATCH topic, and answers every twin
// request the device makes (a GET of the whole twin, a reported-properties
// PATCH) on the response topic, with the status code and the request id the
// device chose in the topic name.
const (
	TwinDesiredPatchTopic = "$iothub/twin/PATCH/properties/desired/#"
	TwinResponseTopic     = "$iothub/twin/res/#"

	twinDesiredPatchPrefix = "$iothub/twin/PATCH/properties/desired/"
	twinResponsePrefix     = "$iothub/twin/res/"
	twinGetTopicFormat     = "$iothub/twin/GET/?$rid=%s"
)

// SettingAck is the device's acknowledgement of one desired property, kept
// under reported.settings_ack.<key> (sc-129688). Status is "applied",
// "rejected" (the value failed validation; Value is the value still in
// effect), "unsupported" (not on the allow-list; ignored) or "cleared" (the
// desired property was removed; Value is the value still in effect).
// DesiredVersion is the desired properties' $version the acknowledgement
// answers, so Rewst can tell an applied change from a pending one.
//
// Every field is always sent: IoT Hub applies a reported PATCH as a recursive
// JSON merge, so a field left out would keep the previous acknowledgement's
// value - an "applied" status beside the last rejection's detail. An empty
// Value is sent as null, which removes it.
type SettingAck struct {
	Value          json.RawMessage `json:"value"`
	Status         string          `json:"status"`
	DesiredVersion int64           `json:"desired_version"`
	Detail         string          `json:"detail"`
}

// SubscribeTwin subscribes to the desired-properties PATCH topic and the twin
// response topic in one SUBSCRIBE, waiting at most timeout for the broker's
// acknowledgement and giving up early when interrupt fires. Both are QoS 0, as
// IoT Hub grants them. handler runs on paho's delivery goroutine and must not
// block.
func SubscribeTwin(
	client Client,
	timeout time.Duration,
	interrupt <-chan struct{},
	handler func(topic string, payload []byte),
) error {
	token := client.SubscribeMultiple(
		map[string]byte{TwinDesiredPatchTopic: 0, TwinResponseTopic: 0},
		func(_ Client, msg Message) { handler(msg.Topic(), msg.Payload()) },
	)
	switch WaitToken(token, timeout, interrupt) {
	case TokenTimedOut:
		return fmt.Errorf("timed out after %v waiting for the twin subscription", timeout)
	case TokenInterrupted:
		return fmt.Errorf("twin subscription abandoned: the connection cycle ended")
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("failed to subscribe to the twin topics: %w", err)
	}
	return nil
}

// RequestTwin asks for the whole twin; the answer arrives on the response
// topic with the same request id.
func RequestTwin(client Client, rid string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = utils.MqttPublishTimeout
	}
	token := client.Publish(fmt.Sprintf(twinGetTopicFormat, rid), 0, false, []byte{})
	if !token.WaitTimeout(timeout) {
		return fmt.Errorf("timed out after %v waiting to request the twin", timeout)
	}
	if err := token.Error(); err != nil {
		return fmt.Errorf("failed to request the twin: %w", err)
	}
	return nil
}

// IsDesiredPatch reports whether topic carries a desired-properties PATCH.
func IsDesiredPatch(topic string) bool {
	return strings.HasPrefix(topic, twinDesiredPatchPrefix)
}

// ParseTwinResponse reads the status code and request id from a twin response
// topic ("$iothub/twin/res/200/?$rid=7"). ok is false for any other topic.
func ParseTwinResponse(topic string) (status int, rid string, ok bool) {
	rest, found := strings.CutPrefix(topic, twinResponsePrefix)
	if !found {
		return 0, "", false
	}
	code, query, _ := strings.Cut(rest, "/")
	status, err := strconv.Atoi(code)
	if err != nil {
		return 0, "", false
	}
	query = strings.TrimPrefix(query, "?")
	for _, kv := range strings.Split(query, "&") {
		if k, v, cut := strings.Cut(kv, "="); cut && k == "$rid" {
			rid = v
		}
	}
	return status, rid, true
}

// TwinDocument is the body of a twin GET response: the desired properties
// (including their $version) and the reported ones.
type TwinDocument struct {
	Desired  json.RawMessage `json:"desired"`
	Reported json.RawMessage `json:"reported"`
}
