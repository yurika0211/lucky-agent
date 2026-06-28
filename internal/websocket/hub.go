package websocket

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/yurika0211/luckyagent/internal/logger"
)

// Client represents a single websocket connection.
type Client struct {
	ID         string
	SessionID  string
	Conn       *websocket.Conn
	Hub        *Hub
	Send       chan *Message
	LastActive time.Time
}

// Hub coordinates websocket clients and session broadcasts.
type Hub struct {
	mu         sync.RWMutex
	clients    map[string]*Client
	sessions   map[string]map[string]bool
	register   chan *Client
	unregister chan *Client
	broadcast  chan *Message
	handler    MessageHandler
	upgrader   websocket.Upgrader
	stats      HubStats
	ctx        context.Context
	cancel     context.CancelFunc
}

// HubStats tracks hub counters.
type HubStats struct {
	mu            sync.RWMutex
	TotalConns    int64
	ActiveConns   int64
	TotalMessages int64
	Errors        int64
}

// MessageHandler processes messages received from a client.
type MessageHandler interface {
	HandleMessage(client *Client, msg *Message)
}

// HubConfig controls websocket timings and buffer sizes.
type HubConfig struct {
	WriteWait       time.Duration
	PongWait        time.Duration
	PingPeriod      time.Duration
	MaxMessageSize  int64
	ReadBufferSize  int
	WriteBufferSize int
}

// DefaultHubConfig returns sane defaults for local websocket chat usage.
func DefaultHubConfig() HubConfig {
	return HubConfig{
		WriteWait:       10 * time.Second,
		PongWait:        60 * time.Second,
		PingPeriod:      54 * time.Second,
		MaxMessageSize:  64 * 1024,
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
	}
}

// NewHub constructs a websocket hub.
func NewHub(handler MessageHandler, cfg HubConfig) *Hub {
	ctx, cancel := context.WithCancel(context.Background())
	return &Hub{
		clients:    make(map[string]*Client),
		sessions:   make(map[string]map[string]bool),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan *Message, 256),
		handler:    handler,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  cfg.ReadBufferSize,
			WriteBufferSize: cfg.WriteBufferSize,
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if origin == "" {
					return true
				}

				originURL, err := url.Parse(origin)
				if err != nil {
					return false
				}

				originHost := originURL.Hostname()
				requestHost := r.Host
				if host, _, err := net.SplitHostPort(r.Host); err == nil {
					requestHost = host
				}

				return sameHostOrLoopback(originHost, requestHost)
			},
		},
		ctx:    ctx,
		cancel: cancel,
	}
}

func sameHostOrLoopback(originHost, requestHost string) bool {
	originHost = strings.TrimSpace(strings.Trim(originHost, "[]"))
	requestHost = strings.TrimSpace(strings.Trim(requestHost, "[]"))
	if originHost == "" || requestHost == "" {
		return false
	}
	if strings.EqualFold(originHost, requestHost) {
		return true
	}
	return isLoopbackHost(originHost) && isLoopbackHost(requestHost)
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Run starts the hub event loop.
func (h *Hub) Run() {
	logger.Info("WebSocket Hub started")
	for {
		select {
		case <-h.ctx.Done():
			logger.Info("WebSocket Hub shutting down")
			h.closeAll()
			return
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client.ID] = client
			if h.sessions[client.SessionID] == nil {
				h.sessions[client.SessionID] = make(map[string]bool)
			}
			h.sessions[client.SessionID][client.ID] = true
			h.mu.Unlock()

			h.stats.mu.Lock()
			h.stats.ActiveConns++
			h.stats.TotalConns++
			h.stats.mu.Unlock()
			logger.Info("client connected", "client_id", client.ID, "session", client.SessionID)

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client.ID]; ok {
				delete(h.clients, client.ID)
				if sess, ok := h.sessions[client.SessionID]; ok {
					delete(sess, client.ID)
					if len(sess) == 0 {
						delete(h.sessions, client.SessionID)
					}
				}
				close(client.Send)
			}
			h.mu.Unlock()

			h.stats.mu.Lock()
			h.stats.ActiveConns--
			h.stats.mu.Unlock()
			logger.Info("client disconnected", "client_id", client.ID, "session", client.SessionID)

		case msg := <-h.broadcast:
			h.mu.RLock()
			if sess, ok := h.sessions[msg.SessionID]; ok {
				for clientID := range sess {
					if client, ok := h.clients[clientID]; ok {
						select {
						case client.Send <- msg:
						default:
							h.mu.RUnlock()
							h.unregister <- client
							h.mu.RLock()
						}
					}
				}
			}
			h.mu.RUnlock()

			h.stats.mu.Lock()
			h.stats.TotalMessages++
			h.stats.mu.Unlock()
		}
	}
}

// Stop stops the hub loop.
func (h *Hub) Stop() {
	h.cancel()
}

func (h *Hub) closeAll() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for id, client := range h.clients {
		close(client.Send)
		client.Conn.Close()
		delete(h.clients, id)
	}
	h.sessions = make(map[string]map[string]bool)
}

// ServeHTTP upgrades the request to websocket and registers the client.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	sessionID := r.URL.Query().Get("session")
	if sessionID == "" {
		sessionID = "default"
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		logger.Error("WebSocket upgrade failed", "error", err)
		return
	}

	client := &Client{
		ID:         generateID(),
		SessionID:  sessionID,
		Conn:       conn,
		Hub:        h,
		Send:       make(chan *Message, 256),
		LastActive: time.Now(),
	}

	h.register <- client
	go client.writePump()
	go client.readPump()
}

// SendToSession broadcasts a message to a session.
func (h *Hub) SendToSession(sessionID string, msg *Message) {
	msg.SessionID = sessionID
	h.broadcast <- msg
}

// SendToClient pushes a message to a single client.
func (h *Hub) SendToClient(clientID string, msg *Message) {
	h.mu.RLock()
	client, ok := h.clients[clientID]
	h.mu.RUnlock()
	if ok {
		select {
		case client.Send <- msg:
		default:
			h.unregister <- client
		}
	}
}

// GetStats returns the current hub stats snapshot.
func (h *Hub) GetStats() HubStats {
	h.stats.mu.RLock()
	defer h.stats.mu.RUnlock()
	return HubStats{
		TotalConns:    h.stats.TotalConns,
		ActiveConns:   h.stats.ActiveConns,
		TotalMessages: h.stats.TotalMessages,
		Errors:        h.stats.Errors,
	}
}

// SessionCount returns the number of active sessions.
func (h *Hub) SessionCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.sessions)
}

// ClientCount returns the number of active clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (c *Client) readPump() {
	defer func() {
		c.Hub.unregister <- c
		c.Conn.Close()
	}()

	c.Conn.SetReadLimit(64 * 1024)
	c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		c.LastActive = time.Now()
		return nil
	})

	for {
		_, raw, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				logger.Error("WebSocket read error", "client_id", c.ID, "error", err)
				c.Hub.stats.mu.Lock()
				c.Hub.stats.Errors++
				c.Hub.stats.mu.Unlock()
			}
			break
		}

		msg, err := ParseMessage(raw)
		if err != nil {
			logger.Error("WebSocket parse error", "client_id", c.ID, "error", err)
			errMsg, _ := NewMessage(TypeError, c.SessionID, ErrorData{
				Code:    "PARSE_ERROR",
				Message: err.Error(),
			})
			c.Send <- errMsg
			continue
		}

		c.LastActive = time.Now()

		if msg.Type == TypePing {
			pong, _ := NewMessage(TypePong, c.SessionID, nil)
			c.Send <- pong
			continue
		}

		if msg.Type == TypeReconnect {
			var data ReconnectData
			if err := msg.ParseData(&data); err == nil {
				logger.Info("client reconnecting", "client_id", c.ID, "last_msg", data.LastMessageID)
			}
			status, _ := NewMessage(TypeStatus, c.SessionID, StatusData{
				State:   "connected",
				Message: "reconnected",
			})
			c.Send <- status
			continue
		}

		if c.Hub.handler != nil {
			c.Hub.handler.HandleMessage(c, msg)
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(54 * time.Second)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			data, err := json.Marshal(msg)
			if err != nil {
				logger.Error("WebSocket marshal error", "client_id", c.ID, "error", err)
				continue
			}

			if err := c.Conn.WriteMessage(websocket.TextMessage, data); err != nil {
				logger.Error("WebSocket write error", "client_id", c.ID, "error", err)
				return
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
