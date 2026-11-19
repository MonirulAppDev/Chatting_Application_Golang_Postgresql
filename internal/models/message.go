// Package models defines the application domain models, database entities, and API payloads.
package models

import (
	"encoding/json"
	"time"
)

/*
Message represents a chat communication entity stored in the database.
*/
type Message struct {
	// Unique primary key identifier
	ID int `json:"id"`
	// User ID who sent the message
	SenderID int `json:"sender_id"`
	// User ID who received the message
	ReceiverID int `json:"receiver_id"`
	// Message category: 'text', 'image', 'audio', 'sticker', 'location'
	MessageType string `json:"message_type"`
	// Message body content or media URL
	Content string `json:"content"`
	// Additional structured metadata (JSONB)
	Metadata json.RawMessage `json:"metadata,omitempty"`
	// Read receipt status
	IsRead bool `json:"is_read"`
	// Timestamp when the message was sent
	CreatedAt time.Time `json:"created_at"`
}
