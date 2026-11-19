// Package websocket manages real-time bi-directional communication, client sessions, and messaging hub.
package websocket

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer (1MB).
	maxMessageSize = 1024 * 1024
)

// Upgrader configures WebSocket connection upgrade parameters and allows cross-origin requests.
var Upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// Allow all origins for mobile apps, emulators, and web clients
		return true
	},
}

// ReadPump runs the read loop for incoming WebSocket messages in the current goroutine.
// When connection closes or an error occurs, the loop terminates and defer ensures
// the client is unregistered from Hub and the connection is closed cleanly.
func (c *Client) ReadPump() {
	defer func() {
		// Remove client from Hub and broadcast user offline presence
		c.Hub.Unregister(c.UserID)
		c.Hub.NotifyUserOffline(context.Background(), c.UserID)
		_ = c.Conn.Close()
	}()

	c.Conn.SetReadLimit(maxMessageSize)
	_ = c.Conn.SetReadDeadline(time.Now().Add(pongWait))
	c.Conn.SetPongHandler(func(string) error {
		_ = c.Conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		// Read incoming message frames from the peer
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("WebSocket connection closed for user %d: %v", c.UserID, err)
			}
			break
		}

		// Route incoming JSON frames based on event type
		var event struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(message, &event); err == nil {
			switch event.Type {
			case "send_message":
				c.Hub.HandleSendMessage(context.Background(), c.UserID, message)
			case "live_location_start":
				c.Hub.HandleLiveLocationStart(context.Background(), c.UserID, message)
			case "live_location_update":
				c.Hub.HandleLiveLocationUpdate(context.Background(), c.UserID, message)
			case "live_location_stop":
				c.Hub.HandleLiveLocationStop(context.Background(), c.UserID, message)
			case "call_offer":
				c.Hub.HandleCallOffer(context.Background(), c.UserID, message)
			case "call_answer":
				c.Hub.HandleCallAnswer(context.Background(), c.UserID, message)
			case "ice_candidate":
				c.Hub.HandleICECandidate(context.Background(), c.UserID, message)
			case "call_reject":
				c.Hub.HandleCallReject(context.Background(), c.UserID, message)
			case "call_end":
				c.Hub.HandleCallEnd(context.Background(), c.UserID, message)
			}
		}
	}
}

// WritePump pumps outgoing messages from the Send channel to the websocket connection using WriteMessage.
// It also sends periodic ping frames to keep the connection alive.
func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The Hub closed the channel.
				_ = c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			// Send message to client via WriteMessage
			if err := c.Conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			_ = c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// WritePumpFunc allows invoking writePump as a standalone function `go WritePump(client)`.
func WritePump(client *Client) {
	client.WritePump()
}
