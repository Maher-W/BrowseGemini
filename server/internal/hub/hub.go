package hub

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type ExtensionRequest struct {
	ID     string `json:"id"`
	Action string `json:"action"`
	Params any    `json:"params"`
}

type ExtensionResponse struct {
	ID      string          `json:"id"`
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type Hub struct {
	adminPassword   string
	upgrader        websocket.Upgrader
	connMu          sync.Mutex
	currentConn     *websocket.Conn
	isConnected     atomic.Bool
	pendingRequests sync.Map // map[string]chan *ExtensionResponse
	reqCounter      atomic.Int64
}

func NewHub(adminPassword string) *Hub {
	return &Hub{
		adminPassword: adminPassword,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
			ReadBufferSize:  1024 * 64,
			WriteBufferSize: 1024 * 64,
		},
	}
}

func (h *Hub) IsConnected() bool {
	return h.isConnected.Load()
}

func (h *Hub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Authenticate via Bearer header or ?token= query parameter
	token := r.URL.Query().Get("token")
	if token == "" {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			token = strings.TrimPrefix(authHeader, "Bearer ")
		}
	}

	// Validate against ADMIN_PASSWORD
	if h.adminPassword == "" || subtle.ConstantTimeCompare([]byte(token), []byte(h.adminPassword)) != 1 {
		log.Printf("[Hub] Unauthorized WebSocket attempt from %s", r.RemoteAddr)
		http.Error(w, "Unauthorized: invalid password", http.StatusUnauthorized)
		return
	}

	conn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[Hub] Failed to upgrade WebSocket: %v", err)
		return
	}

	log.Printf("[Hub] Chrome Extension connected from %s", r.RemoteAddr)

	// Cleanly replace any existing connection
	h.connMu.Lock()
	if h.currentConn != nil {
		log.Printf("[Hub] Closing previous stale connection")
		_ = h.currentConn.Close()
	}
	h.currentConn = conn
	h.isConnected.Store(true)
	h.connMu.Unlock()

	// Start reading messages from extension
	go h.readPump(conn)
	// Start ping heartbeat
	go h.pingPump(conn)
}

func (h *Hub) readPump(conn *websocket.Conn) {
	defer func() {
		h.connMu.Lock()
		if h.currentConn == conn {
			h.currentConn = nil
			h.isConnected.Store(false)
			h.failAllPending("Chrome extension disconnected")
		}
		h.connMu.Unlock()
		_ = conn.Close()
		log.Printf("[Hub] Chrome Extension connection closed")
	}()

	conn.SetReadLimit(10 * 1024 * 1024) // 10MB max message size for screenshots
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error {
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[Hub] Read error: %v", err)
			}
			break
		}

		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))

		var resp ExtensionResponse
		if err := json.Unmarshal(message, &resp); err != nil {
			continue
		}

		if resp.ID != "" {
			if val, ok := h.pendingRequests.LoadAndDelete(resp.ID); ok {
				ch := val.(chan *ExtensionResponse)
				ch <- &resp
			}
		}
	}
}

func (h *Hub) pingPump(conn *websocket.Conn) {
	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		h.connMu.Lock()
		active := (h.currentConn == conn)
		h.connMu.Unlock()

		if !active {
			return
		}

		if err := conn.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(5*time.Second)); err != nil {
			log.Printf("[Hub] Ping write failed: %v", err)
			return
		}
	}
}

func (h *Hub) failAllPending(reason string) {
	h.pendingRequests.Range(func(key, value any) bool {
		h.pendingRequests.Delete(key)
		ch := value.(chan *ExtensionResponse)
		ch <- &ExtensionResponse{
			ID:      key.(string),
			Success: false,
			Error:   reason,
		}
		return true
	})
}

func (h *Hub) SendCommand(ctx context.Context, action string, params any) (*ExtensionResponse, error) {
	if !h.IsConnected() {
		return nil, errors.New("Chrome extension is currently toggled OFF. Ask the user to enable it in their browser.")
	}

	reqID := fmt.Sprintf("req-%d-%d", time.Now().UnixNano(), h.reqCounter.Add(1))
	respChan := make(chan *ExtensionResponse, 1)
	h.pendingRequests.Store(reqID, respChan)
	defer h.pendingRequests.Delete(reqID)

	reqMsg := ExtensionRequest{
		ID:     reqID,
		Action: action,
		Params: params,
	}

	payload, err := json.Marshal(reqMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	h.connMu.Lock()
	conn := h.currentConn
	if conn == nil {
		h.connMu.Unlock()
		return nil, errors.New("Chrome extension is currently toggled OFF. Ask the user to enable it in their browser.")
	}

	_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	err = conn.WriteMessage(websocket.TextMessage, payload)
	h.connMu.Unlock()

	if err != nil {
		return nil, fmt.Errorf("failed to send command to extension: %w", err)
	}

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(35 * time.Second):
		return nil, errors.New("browser action timed out after 35s")
	case resp := <-respChan:
		if !resp.Success {
			errMsg := resp.Error
			if errMsg == "" {
				errMsg = "unknown extension error"
			}
			return nil, errors.New(errMsg)
		}
		return resp, nil
	}
}
