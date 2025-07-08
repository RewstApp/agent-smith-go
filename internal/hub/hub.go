package hub

import (
	"fmt"
	"log"
	"net/http"
)

type hub struct {
	channels    map[string]map[*client]bool
	register    chan *client
	unregister  chan *client
	subscribe   chan subscription
	unsubscribe chan subscription
	publish     chan message
}

type subscription struct {
	client  *client
	channel string
}

type message struct {
	channel string
	data    []byte
}

func newHub() *hub {
	return &hub{
		channels:    make(map[string]map[*client]bool),
		register:    make(chan *client),
		unregister:  make(chan *client),
		subscribe:   make(chan subscription),
		unsubscribe: make(chan subscription),
		publish:     make(chan message),
	}
}

func (h *hub) run() {
	for {
		select {
		case <-h.register:
			log.Println("Client registered")
		case client := <-h.unregister:
			log.Println("Client unregistered")
			for channel := range client.subscribed {
				if subs, ok := h.channels[channel]; ok {
					delete(subs, client)
					if len(subs) == 0 {
						delete(h.channels, channel)
					}
				}
			}
			close(client.send)
		case sub := <-h.subscribe:
			if _, ok := h.channels[sub.channel]; !ok {
				h.channels[sub.channel] = make(map[*client]bool)
			}
			h.channels[sub.channel][sub.client] = true
			sub.client.subscribed[sub.channel] = true
			log.Println("Client subscribed to", sub.channel)
		case sub := <-h.unsubscribe:
			if subs, ok := h.channels[sub.channel]; ok {
				delete(subs, sub.client)
				if len(subs) == 0 {
					delete(h.channels, sub.channel)
				}
			}
			delete(sub.client.subscribed, sub.channel)
			log.Println("Client unsubscribed from", sub.channel)
		case msg := <-h.publish:
			if subs, ok := h.channels[msg.channel]; ok {
				for client := range subs {
					select {
					case client.send <- msg.data:
					default:
						close(client.send)   // ?
						delete(subs, client) // ?
					}
				}
			}
		}
	}
}

func Run(port int) {
	hub := newHub()
	go hub.run()

	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWs(hub, w, r)
	})

	log.Printf("Server on :%d/ws\n", port)
	http.ListenAndServe(fmt.Sprintf(":%d", port), nil)
}
