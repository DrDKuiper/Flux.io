package api

// Client is one connected WebSocket subscriber. Messages are delivered on send;
// a full buffer means the client is too slow and will be dropped.
type Client struct {
	send chan []byte
}

// Hub fans out messages to all connected clients. A single goroutine (Run)
// owns the client set, so register/unregister/broadcast are race-free.
type Hub struct {
	clients    map[*Client]struct{}
	register   chan *Client
	unregister chan *Client
	broadcast  chan []byte
	stop       chan struct{}
}

func NewHub() *Hub {
	return &Hub{
		clients:    make(map[*Client]struct{}),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan []byte, 64),
		stop:       make(chan struct{}),
	}
}

// Run owns the client set until Stop is called. Start it once in a goroutine.
func (h *Hub) Run() {
	for {
		select {
		case <-h.stop:
			return
		case c := <-h.register:
			h.clients[c] = struct{}{}
		case c := <-h.unregister:
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
		case msg := <-h.broadcast:
			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					// Slow client: drop it rather than block the hub.
					delete(h.clients, c)
					close(c.send)
				}
			}
		}
	}
}

// Register adds a client with the given send-buffer size and returns it.
func (h *Hub) Register(buffer int) *Client {
	c := &Client{send: make(chan []byte, buffer)}
	h.register <- c
	return c
}

// Unregister removes a client (safe to call once; the Run loop closes send).
func (h *Hub) Unregister(c *Client) {
	select {
	case h.unregister <- c:
	case <-h.stop:
	}
}

// Broadcast sends msg to all connected clients. Never blocks on a slow client.
func (h *Hub) Broadcast(msg []byte) {
	select {
	case h.broadcast <- msg:
	case <-h.stop:
	}
}

// Stop shuts the hub down.
func (h *Hub) Stop() { close(h.stop) }
