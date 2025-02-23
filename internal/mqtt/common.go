package mqtt

import (
	"context"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type EventType int

const (
	OnError = iota
	OnMessageReceived
	OnConnecting
	OnConnect
	OnConnectionLost
	OnSubscribed
	OnCancelled
)

type Event struct {
	Type    EventType
	Message []byte
	Error   error
}

func waitToken(token mqtt.Token, ctx context.Context) (bool, error) {
	select {
	case <-token.Done():
		// Token completed before cancelling
		return false, token.Error()
	case <-ctx.Done():
		// Cancelled before the token is done
		return true, ctx.Err()
	}
}
