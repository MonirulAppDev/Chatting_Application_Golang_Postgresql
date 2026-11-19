// Package handlers defines the HTTP request handlers and controller logic for the application.
package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"backend/internal/websocket"

	"github.com/golang-jwt/jwt/v5"
)

/*
WSHandler manages real-time WebSocket connection lifecycle, authentication,
and connection upgrades.
*/
type WSHandler struct {
	// Reference to the centralized WebSocket Hub
	Hub *websocket.Hub
	// Secret key retrieved from config used for validating JWT tokens
	JWTSecret string
}

// NewWSHandler creates and returns a new WSHandler instance.
func NewWSHandler(hub *websocket.Hub, jwtSecret string) *WSHandler {
	return &WSHandler{
		Hub:       hub,
		JWTSecret: jwtSecret,
	}
}

// validateWSToken parses and validates the JWT token string and extracts the authenticated user_id.
func (h *WSHandler) validateWSToken(tokenString string) (int, error) {
	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(h.JWTSecret), nil
	})

	if err != nil || !token.Valid {
		return 0, fmt.Errorf("invalid or expired token")
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return 0, fmt.Errorf("invalid token claims")
	}

	switch val := claims["user_id"].(type) {
	case float64:
		return int(val), nil
	case int:
		return val, nil
	case int64:
		return int(val), nil
	default:
		return 0, fmt.Errorf("invalid user_id claim format")
	}
}

// ServeWS handles incoming HTTP requests to upgrade into persistent WebSocket sessions.
// It extracts JWT token from either query parameter (?token=...) or Authorization header,
// authenticates the user, rejects invalid tokens with 401 Unauthorized without upgrading,
// and starts the WebSocket read/write pumps upon successful authentication.
//
// @Summary Connect to real-time WebSocket
// @Description Upgrade HTTP connection to WebSocket protocol. Provide JWT token via query parameter (?token=...) or Authorization header.
// @Tags WebSocket
// @Param token query string false "JWT authentication token"
// @Success 101 {string} string "Switching Protocols to WebSocket"
// @Failure 401 {object} map[string]string "Unauthorized: missing or invalid token"
// @Failure 500 {string} string "Failed to upgrade connection"
// @Router /ws [get]
func (h *WSHandler) ServeWS(w http.ResponseWriter, r *http.Request) {
	/*
		Step 1: Extract JWT Token from Query Param or Authorization Header
	*/
	tokenString := r.URL.Query().Get("token")
	if tokenString == "" {
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenString = strings.TrimPrefix(authHeader, "Bearer ")
		} else if authHeader != "" {
			tokenString = authHeader
		}
	}
	tokenString = strings.TrimSpace(tokenString)

	// If token is missing, reject immediately with 401 Unauthorized without upgrading
	if tokenString == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": "Unauthorized: missing token in query parameter (?token=...) or Authorization header.",
		})
		return
	}

	/*
		Step 2: Authenticate and Extract User ID
		Verify signature and expiration; if invalid, reject with 401 Unauthorized without upgrading
	*/
	userID, err := h.validateWSToken(tokenString)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{
			"error": fmt.Sprintf("Unauthorized: %v", err),
		})
		return
	}

	/*
		Step 3: Upgrade HTTP Connection to WebSocket
	*/
	conn, err := websocket.Upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("Failed to upgrade WebSocket connection for user %d: %v", userID, err)
		return
	}

	/*
		Step 4: Instantiate Client and Register with Hub
	*/
	client := websocket.NewClient(h.Hub, conn, userID)
	h.Hub.Register(client)

	/*
		Step 5: Notify Online Presence to User and Friends
	*/
	h.Hub.NotifyUserOnline(context.Background(), userID)

	/*
		Step 6: Start Write Pump in background and run Read Loop in current goroutine
		The read loop blocks the current HTTP goroutine until the client disconnects.
	*/
	go client.WritePump()
	client.ReadPump()
}
