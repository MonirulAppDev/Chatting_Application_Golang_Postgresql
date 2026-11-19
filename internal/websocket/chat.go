// Package websocket manages real-time bi-directional communication, client sessions, and messaging hub.
package websocket

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"time"
)

/*
SendMessagePayload defines the expected JSON payload for sending a chat message over WebSocket.
*/
type SendMessagePayload struct {
	Type        string `json:"type"`
	ReceiverID  int    `json:"receiver_id"`
	Content     string `json:"content"`
	MessageType string `json:"message_type,omitempty"`
}

/*
MessageSentEvent is sent back to the sender confirming their message was delivered and persisted.
*/
type MessageSentEvent struct {
	Type        string    `json:"type"`
	MessageID   int       `json:"message_id"`
	SenderID    int       `json:"sender_id"`
	ReceiverID  int       `json:"receiver_id"`
	MessageType string    `json:"message_type"`
	Content     string    `json:"content"`
	CreatedAt   time.Time `json:"created_at"`
}

/*
NewMessageEvent is delivered in real-time to the recipient if they are online.
*/
type NewMessageEvent struct {
	Type        string    `json:"type"`
	MessageID   int       `json:"message_id"`
	SenderID    int       `json:"sender_id"`
	ReceiverID  int       `json:"receiver_id"`
	MessageType string    `json:"message_type"`
	Content     string    `json:"content"`
	CreatedAt   time.Time `json:"created_at"`
}

/*
ErrorEvent notifies the sender if a message cannot be processed (e.g., blocked user).
*/
type ErrorEvent struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// sendError helper sends a formatted error JSON event back to the sender.
func (h *Hub) sendError(userID int, errorMsg string) {
	errPayload, err := json.Marshal(ErrorEvent{
		Type:    "error",
		Message: errorMsg,
	})
	if err == nil {
		h.SendToUser(userID, errPayload)
	}
}

// validMessageTypes defines the permitted real-time chat message categories.
var validMessageTypes = map[string]bool{
	"text":    true,
	"image":   true,
	"audio":   true,
	"sticker": true,
}

/*
HandleSendMessage processes incoming "send_message" events:
1. Validates receiver, content/media URL, and message_type (text, image, audio, sticker).
2. Checks the blocks table between sender and receiver.
3. Inserts message into the messages table (stores text content or media URL in content).
4. Dispatches confirmation "message_sent" back to the sender.
5. Performs Smart Notification Check:
   - If receiver is online (Hub.Clients): sends "new_message" event directly (no notification row).
   - If receiver is offline: inserts an unread row into the notifications table with preview.
*/
func (h *Hub) HandleSendMessage(ctx context.Context, senderID int, rawMessage []byte) {
	var payload SendMessagePayload
	if err := json.Unmarshal(rawMessage, &payload); err != nil {
		h.sendError(senderID, "Invalid send_message payload format")
		return
	}

	payload.Content = strings.TrimSpace(payload.Content)
	if payload.ReceiverID <= 0 || payload.Content == "" {
		h.sendError(senderID, "Invalid receiver_id or empty content/media URL")
		return
	}

	// Cannot send message to self
	if senderID == payload.ReceiverID {
		h.sendError(senderID, "You cannot send a message to yourself")
		return
	}

	// Validate and normalize message_type (supports: text, image, audio, sticker)
	messageType := strings.ToLower(strings.TrimSpace(payload.MessageType))
	if messageType == "" {
		messageType = "text"
	}

	if !validMessageTypes[messageType] {
		h.sendError(senderID, "Invalid message_type. Allowed: text, image, audio, sticker")
		return
	}

	/*
		Step 1: Check blocks table between sender and receiver
	*/
	var isBlocked bool
	blockQuery := `
		SELECT EXISTS (
			SELECT 1 FROM blocks
			WHERE (blocker_id = $1 AND blocked_id = $2)
			   OR (blocker_id = $2 AND blocked_id = $1)
		);
	`
	if err := h.DB.QueryRow(ctx, blockQuery, senderID, payload.ReceiverID).Scan(&isBlocked); err != nil {
		log.Printf("Error checking block status between %d and %d: %v", senderID, payload.ReceiverID, err)
		h.sendError(senderID, "Failed to verify user blocking status")
		return
	}

	if isBlocked {
		h.sendError(senderID, "Cannot send message")
		return
	}

	/*
		Step 2: Insert into messages table and retrieve ID & CreatedAt
	*/
	insertMsgQuery := `
		INSERT INTO messages (sender_id, receiver_id, message_type, content, is_read)
		VALUES ($1, $2, $3, $4, FALSE)
		RETURNING id, created_at;
	`
	var messageID int
	var createdAt time.Time

	err := h.DB.QueryRow(ctx, insertMsgQuery, senderID, payload.ReceiverID, messageType, payload.Content).Scan(
		&messageID,
		&createdAt,
	)
	if err != nil {
		log.Printf("Error saving message from %d to %d: %v", senderID, payload.ReceiverID, err)
		h.sendError(senderID, "Failed to save message")
		return
	}

	/*
		Step 3: Send confirmation "message_sent" back to the sender
	*/
	sentEvent := MessageSentEvent{
		Type:        "message_sent",
		MessageID:   messageID,
		SenderID:    senderID,
		ReceiverID:  payload.ReceiverID,
		MessageType: messageType,
		Content:     payload.Content,
		CreatedAt:   createdAt,
	}
	if sentJSON, err := json.Marshal(sentEvent); err == nil {
		h.SendToUser(senderID, sentJSON)
	}

	/*
		Step 4: Smart Notification Check
	*/
	receiverOnline := h.IsUserOnline(payload.ReceiverID)

	if receiverOnline {
		// Receiver is currently online: dispatch "new_message" event directly (do not insert notification)
		newMsgEvent := NewMessageEvent{
			Type:        "new_message",
			MessageID:   messageID,
			SenderID:    senderID,
			ReceiverID:  payload.ReceiverID,
			MessageType: messageType,
			Content:     payload.Content,
			CreatedAt:   createdAt,
		}
		if newMsgJSON, err := json.Marshal(newMsgEvent); err == nil {
			h.SendToUser(payload.ReceiverID, newMsgJSON)
		}
	} else {
		// Receiver is offline: generate appropriate preview based on message_type and insert unread row
		var bodyPreview string
		switch messageType {
		case "image":
			bodyPreview = "📷 Photo"
		case "audio":
			bodyPreview = "🎵 Voice message"
		case "sticker":
			bodyPreview = "👾 Sticker"
		default:
			bodyPreview = payload.Content
			if len(bodyPreview) > 100 {
				bodyPreview = bodyPreview[:97] + "..."
			}
		}

		insertNotificationQuery := `
			INSERT INTO notifications (user_id, sender_id, type, title, body, is_read)
			VALUES ($1, $2, 'message', $3, $4, FALSE);
		`
		if _, err := h.DB.Exec(ctx, insertNotificationQuery, payload.ReceiverID, senderID, "New message", bodyPreview); err != nil {
			log.Printf("Failed to insert offline notification for user %d: %v", payload.ReceiverID, err)
		}
	}

	/*
		Step 5: Background Link Preview (for text messages containing URLs)
	*/
	if messageType == "text" {
		if rawURL := ExtractURL(payload.Content); rawURL != "" {
			go h.fetchAndUpdateLinkPreview(messageID, senderID, payload.ReceiverID, rawURL)
		}
	}
}
