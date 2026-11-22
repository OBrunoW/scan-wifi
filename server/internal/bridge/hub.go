package bridge

import (
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Hub struct {
	register   chan *client
	unregister chan *client
	broadcast  chan Frame
	clients    map[*client]struct{}

	mu     sync.RWMutex
	latest Frame
}

type client struct {
	conn *websocket.Conn
	send chan []byte
}

func NewHub() *Hub {
	return &Hub{
		register:   make(chan *client),
		unregister: make(chan *client),
		broadcast:  make(chan Frame, 64),
		clients:    make(map[*client]struct{}),
	}
}

func (h *Hub) Broadcast(frame Frame) {
	h.mu.Lock()
	h.latest = frame
	h.mu.Unlock()
	select {
	case h.broadcast <- frame:
	default:
		// canal cheio: /api/status já tem latest; só o fanout WS perde este frame
	}
}

func (h *Hub) Run() {
	for {
		select {
		case c := <-h.register:
			h.clients[c] = struct{}{}
		case c := <-h.unregister:
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
		case frame := <-h.broadcast:
			h.mu.Lock()
			h.latest = frame
			h.mu.Unlock()
			data, err := json.Marshal(frame)
			if err != nil {
				continue
			}
			for c := range h.clients {
				select {
				case c.send <- data:
				default:
					close(c.send)
					delete(h.clients, c)
				}
			}
		}
	}
}

func (h *Hub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	c := &client{conn: conn, send: make(chan []byte, 32)}
	h.register <- c

	go c.writePump()
	go c.readPump(h)
}

func (h *Hub) HandleStatus(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	frame := h.latest
	h.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(frame)
}

func (c *client) writePump() {
	defer c.conn.Close()
	for msg := range c.send {
		_ = c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return
		}
	}
}

func (c *client) readPump(h *Hub) {
	defer func() {
		h.unregister <- c
		c.conn.Close()
	}()
	c.conn.SetReadLimit(512)
	_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		_ = c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})
	for {
		if _, _, err := c.conn.ReadMessage(); err != nil {
			return
		}
	}
}
