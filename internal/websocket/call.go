// Package websocket manages real-time bi-directional communication, client sessions, and messaging hub.
package websocket

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

/*
CallOfferPayload defines the incoming JSON structure when a user initiates a call with an SDP offer.
*/
type CallOfferPayload struct {
	Type       string          `json:"type"`
	CallID     string          `json:"call_id,omitempty"` // Unique call identifier (e.g. UUID/timestamp)
	To         int             `json:"to,omitempty"`
	ReceiverID int             `json:"receiver_id,omitempty"`
	CallType   string          `json:"call_type,omitempty"` // "audio" or "video"
	SDP        json.RawMessage `json:"sdp"`
}

/*
CallAnswerPayload defines the incoming JSON structure when a user accepts a call with an SDP answer.
*/
type CallAnswerPayload struct {
	Type       string          `json:"type"`
	CallID     string          `json:"call_id,omitempty"`
	To         int             `json:"to,omitempty"`
	CallerID   int             `json:"caller_id,omitempty"`
	ReceiverID int             `json:"receiver_id,omitempty"`
	SDP        json.RawMessage `json:"sdp"`
}

/*
ICECandidatePayload defines the incoming JSON structure for WebRTC ICE candidate exchange.
*/
type ICECandidatePayload struct {
	Type       string          `json:"type"`
	CallID     string          `json:"call_id,omitempty"`
	To         int             `json:"to,omitempty"`
	ReceiverID int             `json:"receiver_id,omitempty"`
	Candidate  json.RawMessage `json:"candidate"`
}

/*
CallRejectPayload defines the incoming JSON structure when a user declines an incoming call.
*/
type CallRejectPayload struct {
	Type       string `json:"type"`
	CallID     string `json:"call_id,omitempty"`
	To         int    `json:"to,omitempty"`
	CallerID   int    `json:"caller_id,omitempty"`
	ReceiverID int    `json:"receiver_id,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

/*
CallEndPayload defines the incoming JSON structure when a participant hangs up an active call.
*/
type CallEndPayload struct {
	Type            string `json:"type"`
	CallID          string `json:"call_id,omitempty"`
	To              int    `json:"to,omitempty"`
	ReceiverID      int    `json:"receiver_id,omitempty"`
	DurationSeconds int    `json:"duration_seconds,omitempty"`
}

/*
CallOfferRelay is dispatched to the recipient when a call is initiated.
*/
type CallOfferRelay struct {
	Type     string          `json:"type"` // "call_offer"
	CallID   string          `json:"call_id"`
	CallerID int             `json:"caller_id"`
	CallType string          `json:"call_type"`
	SDP      json.RawMessage `json:"sdp"`
}

/*
CallAnswerRelay is dispatched to the caller when the recipient accepts the call.
*/
type CallAnswerRelay struct {
	Type       string          `json:"type"` // "call_answer"
	CallID     string          `json:"call_id,omitempty"`
	SenderID   int             `json:"sender_id"`
	ReceiverID int             `json:"receiver_id"`
	SDP        json.RawMessage `json:"sdp"`
}

/*
ICECandidateRelay is forwarded to the peer during WebRTC connection negotiation.
*/
type ICECandidateRelay struct {
	Type      string          `json:"type"` // "ice_candidate"
	CallID    string          `json:"call_id,omitempty"`
	SenderID  int             `json:"sender_id"`
	Candidate json.RawMessage `json:"candidate"`
}

/*
CallRejectRelay is sent to the caller when a call is declined, blocked, or unavailable.
*/
type CallRejectRelay struct {
	Type     string `json:"type"` // "call_reject"
	CallID   string `json:"call_id,omitempty"`
	SenderID int    `json:"sender_id"`
	Reason   string `json:"reason,omitempty"`
}

/*
CallEndRelay is sent to the peer when a call has been terminated.
*/
type CallEndRelay struct {
	Type            string `json:"type"` // "call_end"
	CallID          string `json:"call_id,omitempty"`
	SenderID        int    `json:"sender_id"`
	DurationSeconds int    `json:"duration_seconds"`
}

// logCall updates or inserts a call log record in the call_logs table.
func (h *Hub) logCall(ctx context.Context, logID, callerID, receiverID int, callType, status string, durationSeconds int) {
	if callType != "video" {
		callType = "audio"
	}
	if status != "missed" && status != "completed" && status != "rejected" {
		status = "completed"
	}
	if durationSeconds < 0 {
		durationSeconds = 0
	}

	if logID > 0 {
		updateQuery := `
			UPDATE call_logs 
			SET status = $1, duration_seconds = $2 
			WHERE id = $3;
		`
		if _, err := h.DB.Exec(ctx, updateQuery, status, durationSeconds, logID); err != nil {
			log.Printf("[Call] Failed to update call log ID %d: %v", logID, err)
		}
		return
	}

	insertQuery := `
		INSERT INTO call_logs (caller_id, receiver_id, call_type, status, duration_seconds)
		VALUES ($1, $2, $3, $4, $5);
	`
	if _, err := h.DB.Exec(ctx, insertQuery, callerID, receiverID, callType, status, durationSeconds); err != nil {
		log.Printf("[Call] Failed to insert call log (%d -> %d, %s, %s): %v", callerID, receiverID, callType, status, err)
	}
}

/*
HandleCallOffer processes incoming "call_offer" event:
1. Validates recipient and call_type ("audio" or "video").
2. Generates/uses unique call_id (timestamp/UUID based).
3. If blocked between caller and recipient, rejects immediately.
4. If recipient is offline:
   - Saves a missed call log in call_logs table with status = 'missed'.
   - Saves a "Missed <call_type> call" notification in notifications table.
   - Notifies caller that user is offline.
5. If recipient is online:
   - Creates a preliminary log entry in call_logs table with status = 'missed' (for later update).
   - Registers session in Hub.ActiveCalls.
   - Forwards call_offer payload (including unique call_id, caller_id, call_type, sdp) to the recipient.
*/
func (h *Hub) HandleCallOffer(ctx context.Context, senderID int, rawMessage []byte) {
	var payload CallOfferPayload
	if err := json.Unmarshal(rawMessage, &payload); err != nil {
		h.sendError(senderID, "Invalid call_offer payload format")
		return
	}

	receiverID := payload.ReceiverID
	if receiverID <= 0 {
		receiverID = payload.To
	}

	if receiverID <= 0 || receiverID == senderID {
		h.sendError(senderID, "Invalid recipient for call_offer")
		return
	}

	callType := strings.ToLower(strings.TrimSpace(payload.CallType))
	if callType != "video" {
		callType = "audio"
	}

	// Generate or use provided unique call_id
	callID := strings.TrimSpace(payload.CallID)
	if callID == "" {
		callID = fmt.Sprintf("call_%d_%d", time.Now().UnixNano(), senderID)
	}

	// 1. Check block relations between caller and recipient
	var isBlocked bool
	blockQuery := `
		SELECT EXISTS (
			SELECT 1 FROM blocks
			WHERE (blocker_id = $1 AND blocked_id = $2)
			   OR (blocker_id = $2 AND blocked_id = $1)
		);
	`
	if err := h.DB.QueryRow(ctx, blockQuery, senderID, receiverID).Scan(&isBlocked); err != nil {
		log.Printf("[Call] Error checking block status between %d and %d: %v", senderID, receiverID, err)
		h.sendError(senderID, "Failed to verify user status")
		return
	}
	if isBlocked {
		rejectPayload, _ := json.Marshal(CallRejectRelay{
			Type:     "call_reject",
			CallID:   callID,
			SenderID: receiverID,
			Reason:   "blocked",
		})
		h.SendToUser(senderID, rejectPayload)
		return
	}

	// 2. If receiver is offline
	if !h.IsUserOnline(receiverID) {
		// Log missed call in call_logs
		h.logCall(ctx, 0, senderID, receiverID, callType, "missed", 0)

		// Save "Missed <call_type> call" notification in notifications table
		notificationTitle := fmt.Sprintf("Missed %s call", callType)
		notificationBody := fmt.Sprintf("Missed %s call", callType)
		insertNotifQuery := `
			INSERT INTO notifications (user_id, sender_id, type, title, body, is_read)
			VALUES ($1, $2, 'call', $3, $4, FALSE);
		`
		if _, err := h.DB.Exec(ctx, insertNotifQuery, receiverID, senderID, notificationTitle, notificationBody); err != nil {
			log.Printf("[Call] Failed to insert missed call notification for user %d: %v", receiverID, err)
		}

		// Notify caller that receiver is offline
		rejectPayload, _ := json.Marshal(CallRejectRelay{
			Type:     "call_reject",
			CallID:   callID,
			SenderID: receiverID,
			Reason:   "user_offline",
		})
		h.SendToUser(senderID, rejectPayload)
		return
	}

	// 3. Receiver is online: Check if receiver is already engaged in an active call
	h.Mutex.Lock()
	if _, inCall := h.ActiveCalls[receiverID]; inCall {
		h.Mutex.Unlock()
		h.logCall(ctx, 0, senderID, receiverID, callType, "rejected", 0)
		busyPayload, _ := json.Marshal(CallRejectRelay{
			Type:     "call_reject",
			CallID:   callID,
			SenderID: receiverID,
			Reason:   "busy",
		})
		h.SendToUser(senderID, busyPayload)
		return
	}

	// 4. Create log entry in call_logs with status = 'missed' for subsequent update
	var logID int
	insertLogQuery := `
		INSERT INTO call_logs (caller_id, receiver_id, call_type, status, duration_seconds)
		VALUES ($1, $2, $3, 'missed', 0)
		RETURNING id;
	`
	if err := h.DB.QueryRow(ctx, insertLogQuery, senderID, receiverID, callType).Scan(&logID); err != nil {
		log.Printf("[Call] Error creating preliminary call log: %v", err)
	}

	// 5. Register active call session
	session := &CallSession{
		CallID:     callID,
		LogID:      logID,
		CallerID:   senderID,
		ReceiverID: receiverID,
		CallType:   callType,
		StartedAt:  time.Now(),
	}
	h.ActiveCalls[senderID] = session
	h.ActiveCalls[receiverID] = session
	h.Mutex.Unlock()

	// 6. Forward call_offer to receiver
	relayEvent := CallOfferRelay{
		Type:     "call_offer",
		CallID:   callID,
		CallerID: senderID,
		CallType: callType,
		SDP:      payload.SDP,
	}
	if relayJSON, err := json.Marshal(relayEvent); err == nil {
		h.SendToUser(receiverID, relayJSON)
	}
}

/*
HandleCallAnswer processes incoming "call_answer" event:
1. Validates call session and extracts call_id, caller_id, and preliminary logID.
2. Marks call started in call_logs table with started_at = NOW() (and updates in-memory session.AcceptedAt).
3. Forwards SDP answer + call_id to caller's WebSocket connection.
*/
func (h *Hub) HandleCallAnswer(ctx context.Context, senderID int, rawMessage []byte) {
	var payload CallAnswerPayload
	if err := json.Unmarshal(rawMessage, &payload); err != nil {
		h.sendError(senderID, "Invalid call_answer payload format")
		return
	}

	targetID := payload.CallerID
	if targetID <= 0 {
		targetID = payload.To
	}
	if targetID <= 0 {
		targetID = payload.ReceiverID
	}

	now := time.Now()
	var callID string
	var logID int

	h.Mutex.Lock()
	session, exists := h.ActiveCalls[senderID]
	if exists && session != nil {
		session.AcceptedAt = &now
		callID = session.CallID
		logID = session.LogID
		if targetID <= 0 {
			if senderID == session.CallerID {
				targetID = session.ReceiverID
			} else {
				targetID = session.CallerID
			}
		}
	}
	h.Mutex.Unlock()

	if targetID <= 0 || targetID == senderID {
		return
	}

	if callID == "" {
		callID = payload.CallID
	}

	// Mark call start time in call_logs table
	if logID > 0 {
		updateLogQuery := `
			UPDATE call_logs 
			SET started_at = $1 
			WHERE id = $2;
		`
		if _, err := h.DB.Exec(ctx, updateLogQuery, now, logID); err != nil {
			log.Printf("[Call] Error marking call started_at for log %d: %v", logID, err)
		}
	}

	relayEvent := CallAnswerRelay{
		Type:       "call_answer",
		CallID:     callID,
		SenderID:   senderID,
		ReceiverID: senderID,
		SDP:        payload.SDP,
	}
	if relayJSON, err := json.Marshal(relayEvent); err == nil {
		h.SendToUser(targetID, relayJSON)
	}
}

/*
HandleICECandidate processes incoming "ice_candidate" event:
Relays WebRTC ICE candidate directly to the peer.
*/
func (h *Hub) HandleICECandidate(ctx context.Context, senderID int, rawMessage []byte) {
	var payload ICECandidatePayload
	if err := json.Unmarshal(rawMessage, &payload); err != nil {
		return
	}

	targetID := payload.ReceiverID
	if targetID <= 0 {
		targetID = payload.To
	}

	var callID string
	if targetID <= 0 {
		h.Mutex.RLock()
		if session, exists := h.ActiveCalls[senderID]; exists && session != nil {
			callID = session.CallID
			if senderID == session.CallerID {
				targetID = session.ReceiverID
			} else {
				targetID = session.CallerID
			}
		}
		h.Mutex.RUnlock()
	}

	if targetID <= 0 || targetID == senderID {
		return
	}

	if callID == "" {
		callID = payload.CallID
	}

	relayEvent := ICECandidateRelay{
		Type:      "ice_candidate",
		CallID:    callID,
		SenderID:  senderID,
		Candidate: payload.Candidate,
	}
	if relayJSON, err := json.Marshal(relayEvent); err == nil {
		h.SendToUser(targetID, relayJSON)
	}
}

/*
HandleCallReject processes incoming "call_reject" event:
1. Removes session from ActiveCalls.
2. Updates preliminary log entry in call_logs to status = 'rejected'.
3. Relays call_reject event to the other party.
*/
func (h *Hub) HandleCallReject(ctx context.Context, senderID int, rawMessage []byte) {
	var payload CallRejectPayload
	if err := json.Unmarshal(rawMessage, &payload); err != nil {
		return
	}

	targetID := payload.CallerID
	if targetID <= 0 {
		targetID = payload.To
	}
	if targetID <= 0 {
		targetID = payload.ReceiverID
	}

	reason := strings.TrimSpace(payload.Reason)
	if reason == "" {
		reason = "declined"
	}

	var session *CallSession
	h.Mutex.Lock()
	if s, exists := h.ActiveCalls[senderID]; exists && s != nil {
		session = s
	} else if payload.CallID != "" {
		for _, s := range h.ActiveCalls {
			if s != nil && s.CallID == payload.CallID {
				session = s
				break
			}
		}
	}

	if session != nil {
		delete(h.ActiveCalls, session.CallerID)
		delete(h.ActiveCalls, session.ReceiverID)
		if targetID <= 0 {
			if senderID == session.CallerID {
				targetID = session.ReceiverID
			} else {
				targetID = session.CallerID
			}
		}
	} else {
		delete(h.ActiveCalls, senderID)
	}
	h.Mutex.Unlock()

	callerID := targetID
	receiverID := senderID
	callType := "audio"
	logID := 0
	callID := payload.CallID

	if session != nil {
		callerID = session.CallerID
		receiverID = session.ReceiverID
		callType = session.CallType
		logID = session.LogID
		if session.CallID != "" {
			callID = session.CallID
		}
	}

	if callerID > 0 && receiverID > 0 {
		h.logCall(ctx, logID, callerID, receiverID, callType, "rejected", 0)
	}

	if targetID > 0 {
		relayEvent := CallRejectRelay{
			Type:     "call_reject",
			CallID:   callID,
			SenderID: senderID,
			Reason:   reason,
		}
		if relayJSON, err := json.Marshal(relayEvent); err == nil {
			h.SendToUser(targetID, relayJSON)
		}
	}
}

/*
HandleCallEnd processes incoming "call_end" event:
1. Calculates call duration in seconds if call was accepted.
2. Removes session from ActiveCalls.
3. Updates the call_logs table ('completed' with duration, or 'missed' if never accepted).
4. Relays call_end event to the peer.
*/
func (h *Hub) HandleCallEnd(ctx context.Context, senderID int, rawMessage []byte) {
	var payload CallEndPayload
	if err := json.Unmarshal(rawMessage, &payload); err != nil {
		return
	}

	targetID := payload.ReceiverID
	if targetID <= 0 {
		targetID = payload.To
	}

	var session *CallSession
	h.Mutex.Lock()
	if s, exists := h.ActiveCalls[senderID]; exists && s != nil {
		session = s
	} else if payload.CallID != "" {
		for _, s := range h.ActiveCalls {
			if s != nil && s.CallID == payload.CallID {
				session = s
				break
			}
		}
	}

	if session != nil {
		delete(h.ActiveCalls, session.CallerID)
		delete(h.ActiveCalls, session.ReceiverID)
		if targetID <= 0 {
			if senderID == session.CallerID {
				targetID = session.ReceiverID
			} else {
				targetID = session.CallerID
			}
		}
	} else {
		delete(h.ActiveCalls, senderID)
	}
	h.Mutex.Unlock()

	callerID := senderID
	receiverID := targetID
	callType := "audio"
	status := "completed"
	durationSeconds := payload.DurationSeconds
	logID := 0
	callID := payload.CallID

	if session != nil {
		callerID = session.CallerID
		receiverID = session.ReceiverID
		callType = session.CallType
		logID = session.LogID
		if session.CallID != "" {
			callID = session.CallID
		}

		if session.AcceptedAt != nil {
			status = "completed"
			calculatedDuration := int(time.Since(*session.AcceptedAt).Seconds())
			if calculatedDuration > durationSeconds {
				durationSeconds = calculatedDuration
			}
		} else if payload.DurationSeconds > 0 {
			status = "completed"
		} else {
			status = "missed"
		}
	}

	if durationSeconds < 0 {
		durationSeconds = 0
	}

	if callerID > 0 && receiverID > 0 {
		h.logCall(ctx, logID, callerID, receiverID, callType, status, durationSeconds)
	}

	if targetID > 0 {
		relayEvent := CallEndRelay{
			Type:            "call_end",
			CallID:          callID,
			SenderID:        senderID,
			DurationSeconds: durationSeconds,
		}
		if relayJSON, err := json.Marshal(relayEvent); err == nil {
			h.SendToUser(targetID, relayJSON)
		}
	}
}
