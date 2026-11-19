// Package websocket manages real-time bi-directional communication, client sessions, and messaging hub.
package websocket

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
)

/*
Client represents an active real-time WebSocket connection for an authenticated user.
It handles outgoing message streaming via a buffered Send channel.
*/
type Client struct {
	// Reference to the central WebSocket hub
	Hub *Hub
	// Authenticated user ID (integer matching users.id in PostgreSQL)
	UserID int
	// Underlying WebSocket connection instance
	Conn *websocket.Conn
	// Buffered channel for outgoing messages queued to be sent to the client
	Send chan []byte
}

// NewClient instantiates a new Client with a buffered Send channel.
func NewClient(hub *Hub, conn *websocket.Conn, userID int) *Client {
	return &Client{
		Hub:    hub,
		UserID: userID,
		Conn:   conn,
		Send:   make(chan []byte, 256),
	}
}

// LocationPoint tracks the latest live location coordinates and target recipient for a user in memory.
type LocationPoint struct {
	Lat       float64   `json:"lat"`
	Lng       float64   `json:"lng"`
	To        int       `json:"to"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CallSession tracks an active call session between two users for duration calculation and logging.
type CallSession struct {
	CallID     string
	LogID      int
	CallerID   int
	ReceiverID int
	CallType   string
	StartedAt  time.Time
	AcceptedAt *time.Time
}

/*
Hub maintains the set of active connected clients and provides thread-safe operations
for registration, unregistration, live location in-memory tracking, and direct message dispatching.
*/
type Hub struct {
	// Active connected clients mapped by their unique user_id
	Clients map[int]*Client

	// In-memory active live location tracking (sender_id -> LocationPoint)
	ActiveLocations map[int]LocationPoint

	// In-memory active call sessions (user_id -> *CallSession)
	ActiveCalls map[int]*CallSession

	// Mutex prevents race conditions when multiple goroutines read/write to maps simultaneously
	Mutex sync.RWMutex

	// Database connection pool for querying friendships, user status, and persistence
	DB *pgxpool.Pool
}

// NewHub initializes and returns a new Hub instance with empty maps and database pool.
func NewHub(db *pgxpool.Pool) *Hub {
	return &Hub{
		Clients:         make(map[int]*Client),
		ActiveLocations: make(map[int]LocationPoint),
		ActiveCalls:     make(map[int]*CallSession),
		DB:              db,
	}
}

// Register adds a connected client to the active clients map in a thread-safe manner.
func (h *Hub) Register(client *Client) {
	if client == nil {
		return
	}
	h.Mutex.Lock()
	defer h.Mutex.Unlock()
	h.Clients[client.UserID] = client
}

// Unregister removes a client from the map by userID, cleans up active location sharing, and closes their Send channel safely.
func (h *Hub) Unregister(userID int) {
	h.Mutex.Lock()
	defer h.Mutex.Unlock()
	if client, ok := h.Clients[userID]; ok {
		delete(h.Clients, userID)
		delete(h.ActiveLocations, userID)
		delete(h.ActiveCalls, userID)
		close(client.Send)
	}
}

// SendToUser dispatches a message payload to a specific user's Send channel non-blockingly.
// It returns true if the message was successfully queued, or false if the user is offline
// or their buffered channel is currently full.
func (h *Hub) SendToUser(userID int, payload []byte) bool {
	h.Mutex.RLock()
	defer h.Mutex.RUnlock()
	if client, ok := h.Clients[userID]; ok {
		select {
		case client.Send <- payload:
			return true
		default:
			// Non-blocking: channel is full or client is slow
			return false
		}
	}
	return false
}

// IsUserOnline checks whether a given user currently has an active WebSocket session.
func (h *Hub) IsUserOnline(userID int) bool {
	h.Mutex.RLock()
	defer h.Mutex.RUnlock()
	_, online := h.Clients[userID]
	return online
}

// Broadcast sends a message payload to all currently connected clients non-blockingly.
func (h *Hub) Broadcast(payload []byte) {
	h.Mutex.RLock()
	defer h.Mutex.RUnlock()
	for _, client := range h.Clients {
		select {
		case client.Send <- payload:
		default:
			// Skip blocking on slow clients
		}
	}
}
