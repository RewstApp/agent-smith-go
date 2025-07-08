package hub

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/gorilla/websocket"
)

type client struct {
	hub        *hub
	conn       *websocket.Conn
	send       chan []byte
	subscribed map[string]bool
}

type payload struct {
	Action  string `json:"action"`
	Channel string `json:"channel"`
	Message string `json:"message,omitempty"`
}

func (c *client) readPump() {
	defer func() {
		c.hub.unregister <- c
		c.conn.Close()
	}()

	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			log.Println("Read error:", err)
			break
		}

		var payload payload
		err = json.Unmarshal(msg, &payload)
		if err != nil {
			log.Println("Invalid JSON:", err)
			continue
		}

		switch payload.Action {
		case "subscribe":
			c.hub.subscribe <- subscription{client: c, channel: payload.Channel}
		case "unsubscribe":
			c.hub.unsubscribe <- subscription{client: c, channel: payload.Channel}
		case "publish":
			c.hub.publish <- message{
				channel: payload.Channel,
				data:    []byte(fmt.Sprintf("[%s]: %s", payload.Channel, payload.Message)),
			}
		default:
			log.Println("Unknown action:", payload.Action)
		}
	}
}

func (c *client) writePump() {
	for msg := range c.send {
		err := c.conn.WriteMessage(websocket.TextMessage, msg)
		if err != nil {
			log.Println("Write error:", err)
			break
		}
	}
}
