// Package websocket manages real-time bi-directional communication, client sessions, and messaging hub.
package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

/*
LiveLocationStartPayload defines the incoming payload when a user begins sharing their live location.
*/
type LiveLocationStartPayload struct {
	Type       string   `json:"type"`
	ReceiverID int      `json:"receiver_id"`
	Latitude   float64  `json:"latitude"`
	Longitude  float64  `json:"longitude"`
	Heading    *float64 `json:"heading,omitempty"`
	Duration   *int     `json:"duration,omitempty"` // Duration in minutes
}

/*
LiveLocationUpdatePayload defines the incoming payload for continuous GPS coordinate updates.
Supports both 'to' / 'receiver_id' and 'lat' / 'latitude' / 'lng' / 'longitude'.
*/
type LiveLocationUpdatePayload struct {
	Type       string  `json:"type"`
	To         int     `json:"to"`
	ReceiverID int     `json:"receiver_id,omitempty"`
	Lat        float64 `json:"lat"`
	Latitude   float64 `json:"latitude,omitempty"`
	Lng        float64 `json:"lng"`
	Longitude  float64 `json:"longitude,omitempty"`
}

/*
LiveLocationStopPayload defines the incoming payload when a user stops sharing live location.
*/
type LiveLocationStopPayload struct {
	Type       string `json:"type"`
	ReceiverID int    `json:"receiver_id"`
}

/*
LiveLocationStartRelay is delivered to the recipient when location sharing begins.
*/
type LiveLocationStartRelay struct {
	Type      string    `json:"type"`
	SenderID  int       `json:"sender_id"`
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	Heading   *float64  `json:"heading,omitempty"`
	Duration  *int      `json:"duration,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

/*
LiveLocationUpdateRelay is delivered in real-time to the recipient for each GPS tick.
*/
type LiveLocationUpdateRelay struct {
	Type      string    `json:"type"`
	SenderID  int       `json:"sender_id"`
	Latitude  float64   `json:"latitude"`
	Longitude float64   `json:"longitude"`
	Speed     *float64  `json:"speed,omitempty"`
	Heading   *float64  `json:"heading,omitempty"`
	Accuracy  *float64  `json:"accuracy,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

/*
LiveLocationStopRelay is delivered to the recipient when location sharing is terminated.
*/
type LiveLocationStopRelay struct {
	Type      string    `json:"type"`
	SenderID  int       `json:"sender_id"`
	Timestamp time.Time `json:"timestamp"`
}

// HandleLiveLocationStart initiates a live location sharing session between sender and receiver.
func (h *Hub) HandleLiveLocationStart(ctx context.Context, senderID int, rawMessage []byte) {
	var payload LiveLocationStartPayload
	if err := json.Unmarshal(rawMessage, &payload); err != nil {
		h.sendError(senderID, "Invalid live_location_start payload")
		return
	}

	if payload.ReceiverID <= 0 {
		h.sendError(senderID, "Invalid receiver_id for live location")
		return
	}

	if senderID == payload.ReceiverID {
		h.sendError(senderID, "Cannot share live location with yourself")
		return
	}

	// Verify block status between sender and receiver
	var isBlocked bool
	blockQuery := `
		SELECT EXISTS (
			SELECT 1 FROM blocks
			WHERE (blocker_id = $1 AND blocked_id = $2)
			   OR (blocker_id = $2 AND blocked_id = $1)
		);
	`
	if err := h.DB.QueryRow(ctx, blockQuery, senderID, payload.ReceiverID).Scan(&isBlocked); err != nil {
		log.Printf("Error checking block status for live location: %v", err)
		h.sendError(senderID, "Database error verifying block status")
		return
	}

	if isBlocked {
		h.sendError(senderID, "Cannot share live location with this user")
		return
	}

	now := time.Now()

	// If receiver is currently online, relay the start event in real-time
	if h.IsUserOnline(payload.ReceiverID) {
		relayEvent := LiveLocationStartRelay{
			Type:      "live_location_start",
			SenderID:  senderID,
			Latitude:  payload.Latitude,
			Longitude: payload.Longitude,
			Heading:   payload.Heading,
			Duration:  payload.Duration,
			Timestamp: now,
		}
		if relayJSON, err := json.Marshal(relayEvent); err == nil {
			h.SendToUser(payload.ReceiverID, relayJSON)
		}
	} else {
		// Receiver is offline: fetch sender username and insert notification
		var senderUsername string
		if err := h.DB.QueryRow(ctx, "SELECT username FROM users WHERE id = $1;", senderID).Scan(&senderUsername); err != nil {
			senderUsername = "A friend"
		}

		notificationBody := fmt.Sprintf("%s started sharing live location", senderUsername)
		insertQuery := `
			INSERT INTO notifications (user_id, sender_id, type, title, body, is_read)
			VALUES ($1, $2, 'live_location', 'Live Location', $3, FALSE);
		`
		if _, err := h.DB.Exec(ctx, insertQuery, payload.ReceiverID, senderID, notificationBody); err != nil {
			log.Printf("Failed to insert live location notification for user %d: %v", payload.ReceiverID, err)
		}
	}

	// Confirm to sender that live location session has started
	confirmation, _ := json.Marshal(map[string]any{
		"type":        "live_location_started",
		"receiver_id": payload.ReceiverID,
		"timestamp":   now,
	})
	h.SendToUser(senderID, confirmation)
}

// HandleLiveLocationUpdate relays real-time GPS coordinate streams to the active recipient
// and updates the in-memory activeLocations map without persisting every tick to the database.
func (h *Hub) HandleLiveLocationUpdate(ctx context.Context, senderID int, rawMessage []byte) {
	var payload LiveLocationUpdatePayload
	if err := json.Unmarshal(rawMessage, &payload); err != nil {
		return
	}

	to := payload.To
	if to <= 0 {
		to = payload.ReceiverID
	}
	lat := payload.Lat
	if lat == 0 {
		lat = payload.Latitude
	}
	lng := payload.Lng
	if lng == 0 {
		lng = payload.Longitude
	}

	if to <= 0 || senderID == to {
		return
	}

	now := time.Now()

	// 1. Update in-memory activeLocations map (NO database write per tick)
	h.Mutex.Lock()
	h.ActiveLocations[senderID] = LocationPoint{
		Lat:       lat,
		Lng:       lng,
		To:        to,
		UpdatedAt: now,
	}
	h.Mutex.Unlock()

	// 2. If receiver is online, relay direct {"type": "live_location", "from": sender_id, "lat": lat, "lng": lng}
	if h.IsUserOnline(to) {
		relayJSON, err := json.Marshal(map[string]any{
			"type": "live_location",
			"from": senderID,
			"lat":  lat,
			"lng":  lng,
		})
		if err == nil {
			h.SendToUser(to, relayJSON)
		}
	}
}

// HandleLiveLocationStop concludes a live location sharing session:
// 1. Deletes the sender's live location entry from the in-memory activeLocations map.
// 2. Persists the final coordinate to the messages table with message_type = 'location'
//    and JSON metadata: {"lat": ..., "lng": ..., "live_ended": true}.
// 3. Relays the live_location_stop event to the recipient if online.
// 4. Sends a confirmation back to the sender.
func (h *Hub) HandleLiveLocationStop(ctx context.Context, senderID int, rawMessage []byte) {
	var payload LiveLocationStopPayload
	if err := json.Unmarshal(rawMessage, &payload); err != nil {
		return
	}

	// 1. Retrieve sender's latest entry and remove from in-memory activeLocations map
	h.Mutex.Lock()
	point, hasPoint := h.ActiveLocations[senderID]
	delete(h.ActiveLocations, senderID)
	h.Mutex.Unlock()

	receiverID := payload.ReceiverID
	if receiverID <= 0 && hasPoint {
		receiverID = point.To
	}

	if receiverID <= 0 || senderID == receiverID {
		return
	}

	now := time.Now()

	// 2. Persist final location point into messages table as a permanent location record
	if hasPoint {
		metadataObj := map[string]any{
			"lat":        point.Lat,
			"lng":        point.Lng,
			"live_ended": true,
		}
		metadataJSON, err := json.Marshal(metadataObj)
		if err == nil {
			insertMsgQuery := `
				INSERT INTO messages (sender_id, receiver_id, message_type, content, metadata, is_read)
				VALUES ($1, $2, 'location', 'Live location ended', $3, FALSE);
			`
			if _, err := h.DB.Exec(ctx, insertMsgQuery, senderID, receiverID, metadataJSON); err != nil {
				log.Printf("Failed to persist final live location message: %v", err)
			}
		}
	}

	// 3. If receiver is online, relay live_location_stop event
	if h.IsUserOnline(receiverID) {
		relayEvent := LiveLocationStopRelay{
			Type:      "live_location_stop",
			SenderID:  senderID,
			Timestamp: now,
		}
		if relayJSON, err := json.Marshal(relayEvent); err == nil {
			h.SendToUser(receiverID, relayJSON)
		}
	}

	// 4. Confirm to sender that live location has stopped
	confirmation, _ := json.Marshal(map[string]any{
		"type":        "live_location_stopped",
		"receiver_id": receiverID,
		"timestamp":   now,
	})
	h.SendToUser(senderID, confirmation)
}
