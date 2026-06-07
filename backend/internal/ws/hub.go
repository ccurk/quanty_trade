package ws

import (
	"encoding/json"
	"log"
	"sync"
	"sync/atomic"

	"github.com/gorilla/websocket"
)

const broadcastBuffer = 1024

type Client struct {
	conn *websocket.Conn
	send chan []byte
}

type Hub struct {
	clients      map[*Client]bool
	broadcast    chan []byte
	register     chan *Client
	unregister   chan *Client
	mu           sync.Mutex
	droppedBcast atomic.Uint64
	kickedSlow   atomic.Uint64
}

func NewHub() *Hub {
	return &Hub{
		broadcast:  make(chan []byte, broadcastBuffer),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		clients:    make(map[*Client]bool),
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			h.mu.Unlock()
		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.send)
			}
			h.mu.Unlock()
		case message := <-h.broadcast:
			h.mu.Lock()
			for client := range h.clients {
				select {
				case client.send <- message:
				default:
					// Slow consumer; kick to avoid backing up the hub.
					// Log every 100 kicks to surface persistent issues
					// without spamming under transient load.
					close(client.send)
					delete(h.clients, client)
					if n := h.kickedSlow.Add(1); n == 1 || n%100 == 0 {
						log.Printf("[WS HUB] kicked slow client (total=%d, remaining_clients=%d)", n, len(h.clients))
					}
				}
			}
			h.mu.Unlock()
		}
	}
}

func (h *Hub) BroadcastJSON(v interface{}) {
	data, err := json.Marshal(v)
	if err != nil {
		log.Printf("Error marshalling JSON for broadcast: %v", err)
		return
	}
	select {
	case h.broadcast <- data:
	default:
		// Broadcast buffer full. Drop the message rather than block the
		// caller (which may be a trading-path goroutine). Log periodically.
		if n := h.droppedBcast.Add(1); n == 1 || n%100 == 0 {
			log.Printf("[WS HUB] broadcast buffer full, dropped message (total dropped=%d, buf=%d)", n, broadcastBuffer)
		}
	}
}

func (c *Client) ReadPump(h *Hub) {
	defer func() {
		h.unregister <- c
		c.conn.Close()
	}()
	for {
		_, _, err := c.conn.ReadMessage()
		if err != nil {
			break
		}
	}
}

func (c *Client) WritePump() {
	defer func() {
		c.conn.Close()
	}()
	for {
		select {
		case message, ok := <-c.send:
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			c.conn.WriteMessage(websocket.TextMessage, message)
		}
	}
}

func HandleConnection(h *Hub, conn *websocket.Conn) {
	client := &Client{conn: conn, send: make(chan []byte, 256)}
	h.register <- client
	go client.WritePump()
	go client.ReadPump(h)
}
